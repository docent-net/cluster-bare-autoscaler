package power

import (
	"context"
	"fmt"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"io"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"log/slog"
	"net/http"
	"time"
)

type WakeOnLanController struct {
	DryRun         bool
	Client         *kubernetes.Clientset
	Nodes          []config.NodeConfig
	Namespace      string
	PodLabel       string
	Port           int
	BootTimeoutSec time.Duration
	BroadcastAddr  string
	MaxRetries     int
}

func (w *WakeOnLanController) PowerOn(ctx context.Context, node string) error {
	var mac string
	for _, n := range w.Nodes {
		if n.Name == node {
			mac = n.WOLMacAddr
			break
		}
	}
	if mac == "" {
		return fmt.Errorf("missing WOL MAC address for node %q", node)
	}

	if w.DryRun {
		slog.Debug("Dry-run: would send WOL request to remote agent", "node", node, "mac", mac, "bcast", w.BroadcastAddr)
		return nil
	}

	ip, err := w.findWOLAgentPodIP(ctx)
	if err != nil {
		return fmt.Errorf("finding WOL agent pod IP: %w", err)
	}

	for attempt := 1; attempt <= w.MaxRetries; attempt++ {
		slog.Info("Sending WOL magic packet via remote agent", "node", node, "mac", mac, "bcast", w.BroadcastAddr, "attempt", attempt)

		if err := w.sendWOLRequest(ctx, ip, mac); err != nil {
			slog.Warn("WOL agent call failed", "node", node, "err", err, "attempt", attempt)
		}

		start := time.Now()
		for time.Since(start) < w.BootTimeoutSec {
			isReady, err := w.checkNodeReady(ctx, node)
			if err != nil {
				slog.Debug("Waiting for node readiness", "node", node, "err", err)
			} else if isReady {
				slog.Info("Node became ready", "node", node)
				return nil
			}
			time.Sleep(5 * time.Second)
		}

		slog.Warn("Node did not become ready after WOL attempt", "node", node, "attempt", attempt, "maxRetries", w.MaxRetries)
	}

	return fmt.Errorf("WOL failed: node %s did not become ready after %d attempts", node, w.MaxRetries)
}

func (w *WakeOnLanController) sendWOLRequest(ctx context.Context, ip string, mac string) error {
	url := fmt.Sprintf("http://%s:%d/wake?mac=%s&broadcast=%s", ip, w.Port, mac, w.BroadcastAddr)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("creating WOL request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending WOL request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("WOL request failed: %s", string(body))
	}

	slog.Debug("WOL request sent successfully", "mac", mac, "url", url)
	return nil
}

func (w *WakeOnLanController) checkNodeReady(ctx context.Context, node string) (bool, error) {
	n, err := w.Client.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	for _, cond := range n.Status.Conditions {
		if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
			return true, nil
		}
	}
	return false, nil
}

func (w *WakeOnLanController) findWOLAgentPodIP(ctx context.Context) (string, error) {
	pods, err := w.Client.CoreV1().Pods(w.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set(map[string]string{
			"app": w.PodLabel,
		}).String(),
	})
	if err != nil {
		return "", fmt.Errorf("listing WOL agent pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no WOL agent pod found in namespace %s with label %s", w.Namespace, w.PodLabel)
	}

	return pods.Items[0].Status.PodIP, nil
}
