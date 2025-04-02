package strategy

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corefake "k8s.io/client-go/kubernetes/fake"
)

func TestLoadAverageScaleUp_DryRunOverride(t *testing.T) {
	strategy := newTestUpStrategyWithDefaults(func(s *LoadAverageScaleUp) {
		s.DryRunOverride = ptr(0.8)
		s.ClusterWideThreshold = 0.6
	})

	node, ok, err := strategy.ShouldScaleUp(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || node != "node-a" {
		t.Errorf("expected scale-up with node-a, got: %s, ok=%v", node, ok)
	}
}

func TestLoadAverageScaleUp_DryRunBelowThreshold(t *testing.T) {
	strategy := newTestUpStrategyWithDefaults(func(s *LoadAverageScaleUp) {
		s.DryRunOverride = ptr(0.4)
		s.ClusterWideThreshold = 0.6
	})

	_, ok, err := strategy.ShouldScaleUp(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-up to be skipped due to low override value")
	}
}

func TestLoadAverageScaleUp_NoCandidates(t *testing.T) {
	strategy := newTestUpStrategyWithDefaults(func(s *LoadAverageScaleUp) {
		s.ShutdownCandidates = func(ctx context.Context) []string { return nil }
		s.DryRunOverride = ptr(0.9)
		s.ClusterWideThreshold = 0.6
	})

	_, ok, err := strategy.ShouldScaleUp(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-up to be skipped due to no shutdown candidates")
	}
}

func TestLoadAverageScaleUp_WithClusterLoadEval(t *testing.T) {
	strategy := newTestUpStrategyWithDefaults(func(s *LoadAverageScaleUp) {
		s.ClusterWideThreshold = 0.6
		s.ClusterEvalMode = ClusterEvalAverage
		s.DryRunOverride = ptr(0.7) // simulate real cluster-wide load using override
	})

	node, ok, err := strategy.ShouldScaleUp(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || node != "node-a" {
		t.Errorf("expected scale-up with node-a, got: %s, ok=%v", node, ok)
	}
}

func newTestUpStrategyWithDefaults(opts ...func(*LoadAverageScaleUp)) *LoadAverageScaleUp {
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}

	client := corefake.NewSimpleClientset(node)

	strategy := &LoadAverageScaleUp{
		Client:               client,
		Namespace:            "default",
		PodLabel:             "app=test-metrics",
		HTTPPort:             9100,
		HTTPTimeout:          time.Second,
		ClusterEvalMode:      ClusterEvalNone,
		ClusterWideThreshold: 0.6,
		IgnoreLabels:         map[string]string{},
		ShutdownCandidates: func(ctx context.Context) []string {
			return []string{"node-a"}
		},
	}

	for _, opt := range opts {
		opt(strategy)
	}

	return strategy
}
