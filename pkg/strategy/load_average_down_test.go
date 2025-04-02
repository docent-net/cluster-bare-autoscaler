package strategy

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	corefake "k8s.io/client-go/kubernetes/fake"
)

func TestDryRunOverride(t *testing.T) {
	override := 0.3

	strategy := newTestStrategyWithDefaults(t, "dummy-node", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = &override
		s.ClusterEvalMode = ClusterEvalAverage
		s.ClusterWideThreshold = 0.5
		s.DryRunClusterLoadOverride = ptr(0.3)
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "dummy-node")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected scale down to be allowed with override=0.3 < 0.5")
	}
}

func TestShouldScaleDown_ClusterEvalAverage(t *testing.T) {
	strategy := newTestStrategyWithDefaults(t, "node1", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = ptr(0.4)
		s.ClusterEvalMode = ClusterEvalAverage
		s.ClusterWideThreshold = 0.5
		s.DryRunClusterLoadOverride = ptr(0.55) // aggregate = 0.55 (too high)
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected node1 to NOT be eligible due to high cluster-wide load (0.55 ≥ 0.5)")
	}
}

func TestShouldScaleDown_DryRunOverrideWins(t *testing.T) {
	override := 0.3

	strategy := newTestStrategyWithDefaults(t, "node1", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = &override
		s.ClusterEvalMode = ClusterEvalAverage
		s.ClusterWideThreshold = 0.5
		s.DryRunClusterLoadOverride = ptr(0.3)
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected node1 to be eligible for scale-down due to override")
	}
}

func TestShouldScaleDown_NoClusterData(t *testing.T) {
	override := 0.3

	strategy := newTestStrategyWithDefaults(t, "node1", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = &override
		s.ClusterEvalMode = ClusterEvalMedian
		s.DryRunClusterLoadOverride = ptr(0.0) // Simulate zero aggregate load
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-down to be denied due to lack of cluster data")
	}
}

func TestShouldScaleDown_ThresholdBlocks(t *testing.T) {
	override := 1.0
	strategy := &LoadAverageScaleDown{
		DryRunNodeLoadOverride: &override,
		NodeThreshold:          0.5,
	}

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-down to be denied due to high override load")
	}
}

func TestShouldScaleDown_ClusterWideThresholdBlocks(t *testing.T) {
	override := 0.3

	strategy := newTestStrategyWithDefaults(t, "node1", func(s *LoadAverageScaleDown) {
		s.DryRunNodeLoadOverride = &override
		s.NodeThreshold = 0.5
		s.ClusterWideThreshold = 0.4
		s.ClusterEvalMode = ClusterEvalAverage
		s.DryRunClusterLoadOverride = ptr(0.55) // aggregate = 0.55 (too high)
	})

	ok, err := strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-down to be denied due to high cluster-wide load (0.55 ≥ 0.4)")
	}

	// Now test passing cluster-wide threshold
	strategy.DryRunClusterLoadOverride = ptr(0.25) // aggregate = 0.25 (ok)

	ok, err = strategy.ShouldScaleDown(context.Background(), "node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected scale-down to be allowed (0.25 < 0.4)")
	}
}

func TestAggregationFunctions(t *testing.T) {
	cases := []struct {
		name     string
		fn       func([]float64) float64
		input    []float64
		expected float64
	}{
		{"Average of 1,2,3", average, []float64{1, 2, 3}, 2.0},
		{"Median odd", median, []float64{5, 1, 3}, 3.0},
		{"Median even", median, []float64{1, 2, 3, 4}, 2.5},
		{"P90 typical", p90, []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 9.1},
		{"P90 short", p90, []float64{10, 20, 30}, 28.0},
		{"P75 typical", p75, []float64{10, 20, 30, 40}, 32.5},
		{"Empty average", average, []float64{}, 0},
		{"Empty median", median, []float64{}, 0},
		{"Empty p90", p90, []float64{}, 0},
		{"Empty p75", p75, []float64{}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fn(tc.input)
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func newTestStrategyWithDefaults(t *testing.T, name string, opts ...func(*LoadAverageScaleDown)) *LoadAverageScaleDown {
	t.Helper()

	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	peerNode := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: name + "-peer"}}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-pod-" + name,
			Namespace: "default",
			Labels:    map[string]string{"app": "test-metrics"},
		},
		Spec:   v1.PodSpec{NodeName: name},
		Status: v1.PodStatus{PodIP: "127.0.0.1"},
	}

	peerPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-pod-" + name + "-peer",
			Namespace: "default",
			Labels:    map[string]string{"app": "test-metrics"},
		},
		Spec:   v1.PodSpec{NodeName: name + "-peer"},
		Status: v1.PodStatus{PodIP: "127.0.0.1"},
	}

	strategy := &LoadAverageScaleDown{
		Client:          corefake.NewSimpleClientset(node, peerNode, pod, peerPod),
		Namespace:       "default",
		PodLabel:        "app=test-metrics",
		HTTPPort:        9100,
		HTTPTimeout:     1 * time.Second,
		NodeThreshold:   0.5,
		ClusterEvalMode: ClusterEvalNone,
		IgnoreLabels:    map[string]string{},
	}

	for _, opt := range opts {
		opt(strategy)
	}

	return strategy
}

func ptr[T any](v T) *T {
	return &v
}
