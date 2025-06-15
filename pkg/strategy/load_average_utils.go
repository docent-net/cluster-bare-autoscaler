package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	"log/slog"
	"net/http"
	"sort"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ClusterLoadEvalMode string

const (
	ClusterEvalNone    ClusterLoadEvalMode = ""
	ClusterEvalAverage ClusterLoadEvalMode = "average"
	ClusterEvalMedian  ClusterLoadEvalMode = "median"
	ClusterEvalP90     ClusterLoadEvalMode = "p90"
	ClusterEvalP75     ClusterLoadEvalMode = "p75"
)

var evalFuncs = map[ClusterLoadEvalMode]func([]float64) float64{
	ClusterEvalAverage: average,
	ClusterEvalMedian:  median,
	ClusterEvalP90:     p90,
	ClusterEvalP75:     p75,
}

type ClusterLoadUtils struct {
	Client      kubernetes.Interface
	Namespace   string
	PodLabel    string
	HTTPPort    int
	HTTPTimeout time.Duration
}

func NewClusterLoadUtils(client kubernetes.Interface, ns, label string, port int, timeout time.Duration) *ClusterLoadUtils {
	return &ClusterLoadUtils{
		Client:      client,
		Namespace:   ns,
		PodLabel:    label,
		HTTPPort:    port,
		HTTPTimeout: timeout,
	}
}

func (u *ClusterLoadUtils) GetEligibleClusterLoads(ctx context.Context, ignore map[string]string, exclude string) ([]float64, map[string]float64, error) {
	nodes, err := u.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}

	var names []string
	for _, n := range nodes.Items {
		if _, isDown := n.Annotations[nodeops.AnnotationPoweredOff]; isDown {
			slog.Debug("Skipping load fetch: node has powered-off annotation", "node", n.Name)
			continue
		}

		if n.Name != exclude && !nodeops.ShouldIgnoreNodeDueToLabels(n, ignore) {
			names = append(names, n.Name)
		}
	}
	return u.FetchClusterLoads(ctx, names)
}

func (u *ClusterLoadUtils) FetchClusterLoads(ctx context.Context, nodeNames []string) ([]float64, map[string]float64, error) {
	var results []float64
	var loads []float64
	nodeToLoad := make(map[string]float64)

	for _, name := range nodeNames {
		load, err := u.FetchNormalizedLoad(ctx, name)
		if err != nil {
			slog.Warn("Skipping node due to error", "node", name, "err", err)
			continue
		}
		loads = append(loads, load)
		nodeToLoad[name] = load

		results = append(results, load)
	}
	return loads, nodeToLoad, nil
}

func (u *ClusterLoadUtils) FetchNormalizedLoad(ctx context.Context, nodeName string) (float64, error) {
	pod, err := u.findMetricsPodForNode(ctx, nodeName)
	if err != nil {
		return 0, fmt.Errorf("finding metrics pod: %w", err)
	}

	url := fmt.Sprintf("http://%s:%d/load", pod.Status.PodIP, u.HTTPPort)
	reqCtx, cancel := context.WithTimeout(ctx, u.HTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("calling load endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	var data struct {
		Load15   float64 `json:"load15"`
		CPUCount int     `json:"cpuCount"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("decode failed: %w", err)
	}
	if data.CPUCount == 0 {
		return 0, errors.New("CPUCount is zero")
	}
	return data.Load15 / float64(data.CPUCount), nil
}

func (u *ClusterLoadUtils) findMetricsPodForNode(ctx context.Context, nodeName string) (*v1.Pod, error) {
	pods, err := u.Client.CoreV1().Pods(u.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: u.PodLabel,
	})
	if err != nil {
		return nil, err
	}

	for _, p := range pods.Items {
		if p.Spec.NodeName == nodeName && p.Status.PodIP != "" {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("no metrics pod for node %s", nodeName)
}

func EvaluateAggregate(loads []float64, mode ClusterLoadEvalMode) float64 {
	if fn := evalFuncs[mode]; fn != nil {
		return fn(loads)
	}
	return average(loads)
}

func ParseClusterEvalMode(mode string) ClusterLoadEvalMode {
	switch mode {
	case "median":
		return ClusterEvalMedian
	case "p90":
		return ClusterEvalP90
	case "p75":
		return ClusterEvalP75
	default:
		return ClusterEvalAverage
	}
}

// Stats
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
	return percentile(values, 0.5)
}

func p90(values []float64) float64 {
	return percentile(values, 0.9)
}

func p75(values []float64) float64 {
	return percentile(values, 0.75)
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	pos := p * float64(len(sorted)-1)
	lower := int(pos)
	upper := lower + 1
	if upper >= len(sorted) {
		return sorted[lower]
	}
	weight := pos - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func (u *ClusterLoadUtils) GetClusterAggregateLoad(
	ctx context.Context,
	ignoreLabels map[string]string,
	excludeNode string,
	override *float64,
	mode ClusterLoadEvalMode,
) (float64, error) {
	if override != nil {
		slog.Info("Dry-run override: using cluster-wide load", "value", *override)
		return *override, nil
	}

	loads, nodeLoads, err := u.GetEligibleClusterLoads(ctx, ignoreLabels, excludeNode)
	if err != nil || len(loads) == 0 {
		slog.Warn("No eligible cluster load data available", "err", err)
		return 0, fmt.Errorf("no cluster load data")
	}

	for node, val := range nodeLoads {
		slog.Debug("Cluster load sample", "node", node, "normalizedLoad", val)
	}

	return EvaluateAggregate(loads, mode), nil
}
