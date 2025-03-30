package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log/slog"
	"net/http"
	"time"

	v1 "k8s.io/api/core/v1"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"k8s.io/client-go/kubernetes"
)

type LoadAverageScaleDown struct {
	Client         kubernetes.Interface
	Cfg            *config.Config
	PodLabel       string
	Namespace      string
	HTTPPort       int
	HTTPTimeout    time.Duration
	Threshold      float64
	DryRunOverride *float64 // Optional CLI override for normalized load
}

func (l *LoadAverageScaleDown) ShouldScaleDown(ctx context.Context, nodeName string) (bool, error) {
	var normalized float64
	var err error

	if l.DryRunOverride != nil {
		normalized = *l.DryRunOverride
		slog.Info("Dry-run override: using normalized load value", "node", nodeName, "value", normalized)
	} else {
		normalized, err = l.fetchNormalizedLoad(ctx, nodeName)
		if err != nil {
			return false, fmt.Errorf("fetching load for node %s: %w", nodeName, err)
		}
	}

	if normalized < l.Threshold {
		return true, nil
	}
	slog.Info("Node load too high to scale down", "node", nodeName, "normalizedLoad", normalized, "threshold", l.Threshold)
	return false, nil
}

func (l *LoadAverageScaleDown) fetchNormalizedLoad(ctx context.Context, nodeName string) (float64, error) {
	pod, err := l.findMetricsPodForNode(ctx, nodeName)
	if err != nil {
		return 0, fmt.Errorf("finding metrics pod: %w", err)
	}

	url := fmt.Sprintf("http://%s:%d/load", pod.Status.PodIP, l.HTTPPort)
	reqCtx, cancel := context.WithTimeout(ctx, l.HTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("calling load endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected response status: %s", resp.Status)
	}

	var data struct {
		Load15   float64 `json:"load15"`
		CPUCount int     `json:"cpuCount"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("decoding response: %w", err)
	}

	if data.CPUCount == 0 {
		return 0, errors.New("invalid metrics: CPU count is zero")
	}

	normalized := data.Load15 / float64(data.CPUCount)
	slog.Debug("Fetched load metrics", "node", nodeName, "load15", data.Load15, "cpuCount", data.CPUCount, "normalized", normalized)
	return normalized, nil
}

func (l *LoadAverageScaleDown) findMetricsPodForNode(ctx context.Context, nodeName string) (*v1.Pod, error) {
	pods, err := l.Client.CoreV1().Pods(l.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: l.PodLabel,
	})
	if err != nil {
		return nil, err
	}

	for _, pod := range pods.Items {
		if pod.Spec.NodeName == nodeName && pod.Status.PodIP != "" {
			return &pod, nil
		}
	}

	return nil, fmt.Errorf("no metrics pod found on node %s", nodeName)
}
