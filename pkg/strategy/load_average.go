package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log/slog"
	"net/http"
	"sort"
	"time"

	v1 "k8s.io/api/core/v1"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"k8s.io/client-go/kubernetes"
)

type LoadAverageScaleDown struct {
	Client          kubernetes.Interface
	Cfg             *config.Config
	PodLabel        string
	Namespace       string
	HTTPPort        int
	HTTPTimeout     time.Duration
	Threshold       float64
	DryRunOverride  *float64 // Optional CLI override for normalized load
	ClusterEvalMode ClusterLoadEvalMode
	IgnoreLabels    map[string]string
	LoadFetcher     ClusterLoadFetcher
}

type ClusterLoadFetcher interface {
	FetchClusterLoads(ctx context.Context, nodeNames []string) ([]float64, error)
}

type DefaultClusterLoadFetcher struct {
	Strategy *LoadAverageScaleDown
}

func (f *DefaultClusterLoadFetcher) FetchClusterLoads(ctx context.Context, nodeNames []string) ([]float64, error) {
	return f.Strategy.fetchClusterLoads(ctx, nodeNames)
}

type ClusterLoadEvalMode string

const (
	ClusterEvalNone    ClusterLoadEvalMode = ""
	ClusterEvalAverage ClusterLoadEvalMode = "average"
	ClusterEvalMedian  ClusterLoadEvalMode = "median"
	ClusterEvalP90     ClusterLoadEvalMode = "p90"
)

var evalFuncs = map[ClusterLoadEvalMode]func([]float64) float64{
	ClusterEvalAverage: average,
	ClusterEvalMedian:  median,
	ClusterEvalP90:     p90,
}

func (l *LoadAverageScaleDown) ShouldScaleDown(ctx context.Context, nodeName string) (bool, error) {
	normalized, err := l.getNormalizedLoadForNode(ctx, nodeName)
	if err != nil {
		return false, err
	}

	if normalized >= l.Threshold {
		slog.Info("Node load too high to scale down", "node", nodeName, "normalizedLoad", normalized, "threshold", l.Threshold)
		return false, nil
	}

	if l.ClusterEvalMode != ClusterEvalNone {
		clusterLoads, err := l.getEligibleClusterLoads(ctx, nodeName)
		if err != nil || len(clusterLoads) == 0 {
			slog.Warn("No cluster-wide load data available", "err", err)
			return false, nil
		}

		aggregate := l.evaluateClusterAggregate(clusterLoads)
		slog.Info("Cluster-wide load average",
			"aggregate", aggregate,
			"mode", l.ClusterEvalMode.String(),
			"candidateLoad", normalized,
		)

		if normalized >= aggregate {
			slog.Info("Node load is not below cluster-wide aggregate — skipping scale-down", "node", nodeName)
			return false, nil
		}
	}

	return true, nil
}

func (l *LoadAverageScaleDown) getNormalizedLoadForNode(ctx context.Context, nodeName string) (float64, error) {
	if l.DryRunOverride != nil {
		slog.Info("Dry-run override: using normalized load value", "node", nodeName, "value", *l.DryRunOverride)
		return *l.DryRunOverride, nil
	}
	return l.fetchNormalizedLoad(ctx, nodeName)
}

func (l *LoadAverageScaleDown) getEligibleClusterLoads(ctx context.Context, excludeNode string) ([]float64, error) {
	nodes, err := l.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var eligible []string
	for _, node := range nodes.Items {
		if node.Name != excludeNode && !shouldIgnoreNode(node, l.IgnoreLabels) {
			eligible = append(eligible, node.Name)
		}
	}

	return l.fetchClusterLoads(ctx, eligible)
}

func (l *LoadAverageScaleDown) evaluateClusterAggregate(loads []float64) float64 {
	evalFunc := evalFuncs[l.ClusterEvalMode]
	if evalFunc == nil {
		return average(loads)
	}
	return evalFunc(loads)
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

func (l *LoadAverageScaleDown) Name() string {
	return "LoadAverage"
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

func (l *LoadAverageScaleDown) fetchClusterLoads(ctx context.Context, eligibleNodeNames []string) ([]float64, error) {
	var results []float64
	for _, name := range eligibleNodeNames {
		load, err := l.fetchNormalizedLoad(ctx, name)
		if err != nil {
			slog.Warn("Skipping node for cluster load average due to error", "node", name, "err", err)
			continue
		}
		results = append(results, load)
	}
	return results, nil
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64{}, values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

func p90(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64{}, values...)
	sort.Float64s(sorted)
	idx := int(float64(len(sorted)) * 0.9)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func shouldIgnoreNode(node v1.Node, ignoreLabels map[string]string) bool {
	for key, val := range ignoreLabels {
		if nodeVal, ok := node.Labels[key]; ok && nodeVal == val {
			return true
		}
	}
	return false
}

func (m ClusterLoadEvalMode) String() string {
	return string(m)
}

func ParseClusterEvalMode(mode string) ClusterLoadEvalMode {
	switch mode {
	case "median":
		return ClusterEvalMedian
	case "p90":
		return ClusterEvalP90
	default:
		return ClusterEvalAverage
	}
}
