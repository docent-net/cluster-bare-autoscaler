package controller_test

import (
	"github.com/docent-net/cluster-bare-autoscaler/pkg/controller"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	"testing"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"context"

	"k8s.io/client-go/kubernetes/fake"
)

func makeNode(name string, labels map[string]string) v1.Node {
	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

type mockScaleDownStrategy struct{}

func (m *mockScaleDownStrategy) ShouldScaleDown(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (m *mockScaleDownStrategy) Name() string { return "mock" }

func TestGetEligibleNodes_Shuffling(t *testing.T) {
	r := &controller.Reconciler{
		Cfg:   mockConfig(),
		State: nodeops.NewNodeStateTracker(),
	}

	nodes := []v1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "node1"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node2"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node3"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node4"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node5"}},
	}

	pickedLast := map[string]bool{}

	for i := 0; i < 100; i++ {
		eligible := nodeops.FilterShutdownEligibleNodes(nodes, r.State, time.Now(), nodeops.EligibilityConfig{
			Cooldown:     r.Cfg.Cooldown,
			BootCooldown: r.Cfg.BootCooldown,
			IgnoreLabels: r.Cfg.IgnoreLabels,
		})
		last := eligible[len(eligible)-1].Name
		pickedLast[last] = true
		if len(pickedLast) >= 3 {
			break
		}
	}

	if len(pickedLast) < 3 {
		t.Errorf("Shuffling appears ineffective, only got %v as final candidate", pickedLast)
	}
}

func mockConfig() *config.Config {
	return &config.Config{
		Cooldown:     time.Minute,
		IgnoreLabels: map[string]string{},
	}
}

func TestGetEligibleNodes(t *testing.T) {
	cfg := &config.Config{
		IgnoreLabels: map[string]string{
			"node-role.kubernetes.io/control-plane": "",
		},
	}
	r := &controller.Reconciler{
		Cfg:   cfg,
		State: nodeops.NewNodeStateTracker(),
	}

	nodes := []v1.Node{
		makeNode("node1", map[string]string{}),
		makeNode("cp1", map[string]string{"node-role.kubernetes.io/control-plane": ""}),
		makeNode("node2", map[string]string{}),
	}

	eligible := nodeops.FilterShutdownEligibleNodes(nodes, r.State, time.Now(), nodeops.EligibilityConfig{
		Cooldown:     r.Cfg.Cooldown,
		BootCooldown: r.Cfg.BootCooldown,
		IgnoreLabels: r.Cfg.IgnoreLabels,
	})
	if len(eligible) != 2 {
		t.Errorf("expected 2 eligible nodes, got %d", len(eligible))
	}
}

func TestPickScaleDownCandidate(t *testing.T) {
	cfg := &config.Config{
		MinNodes: 2,
	}
	r := &controller.Reconciler{
		Cfg:   cfg,
		State: nodeops.NewNodeStateTracker(),
	}

	nodes := []v1.Node{
		makeNode("node1", nil),
		makeNode("node2", nil),
		makeNode("node3", nil),
	}

	candidate := r.PickScaleDownCandidate(
		nodeops.WrapNodes(nodes, r.State, time.Now(), nodeops.NodeAnnotationConfig{}, r.Cfg.IgnoreLabels),
	)
	if candidate == nil || candidate.Name != "node3" {
		t.Errorf("expected node3 as candidate, got %v", candidate)
	}

	nodes = nodes[:2]
	candidate = r.PickScaleDownCandidate(
		nodeops.WrapNodes(nodes, r.State, time.Now(), nodeops.NodeAnnotationConfig{}, r.Cfg.IgnoreLabels),
	)
	if candidate != nil {
		t.Errorf("expected nil candidate when at or below MinNodes, got %v", candidate)
	}
}

func TestCooldownExclusion(t *testing.T) {
	cfg := &config.Config{
		Cooldown: 5 * time.Minute,
	}
	state := nodeops.NewNodeStateTracker()
	r := &controller.Reconciler{Cfg: cfg, State: state}

	node := makeNode("node1", nil)
	// Simulate recent shutdown 1 minute ago
	state.MarkShutdown("node1")
	state.SetShutdownTime("node1", time.Now().Add(-1*time.Minute))

	nodes := []v1.Node{node}
	eligible := nodeops.FilterShutdownEligibleNodes(nodes, r.State, time.Now(), nodeops.EligibilityConfig{
		Cooldown:     r.Cfg.Cooldown,
		BootCooldown: r.Cfg.BootCooldown,
		IgnoreLabels: r.Cfg.IgnoreLabels,
	})
	if len(eligible) != 0 {
		t.Errorf("expected 0 eligible nodes due to cooldown, got %d", len(eligible))
	}
}

func TestPoweredOffNodeIsExcluded(t *testing.T) {
	r := &controller.Reconciler{
		Cfg:   &config.Config{},
		State: nodeops.NewNodeStateTracker(),
	}
	node := makeNode("node2", nil)
	r.State.MarkPoweredOff("node2")

	nodes := []v1.Node{node}
	eligible := nodeops.FilterShutdownEligibleNodes(nodes, r.State, time.Now(), nodeops.EligibilityConfig{
		Cooldown:     r.Cfg.Cooldown,
		BootCooldown: r.Cfg.BootCooldown,
		IgnoreLabels: r.Cfg.IgnoreLabels,
	})
	if len(eligible) != 0 {
		t.Errorf("expected 0 eligible nodes due to powered-off status, got %d", len(eligible))
	}
}

type mockMetrics struct{}

func (m *mockMetrics) RecordEligibleNodes(_ int) {}
func (m *mockMetrics) RecordChosenNode(_ string) {}

type mockShutdowner struct{}

func (m *mockShutdowner) Shutdown(_ context.Context, _ string) error { return nil }

func TestMaybeScaleDown_DryRun(t *testing.T) {
	client := fake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
		},
	})
	state := nodeops.NewNodeStateTracker()

	n := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node1"}}
	wrapped := nodeops.NewNodeWrapper(n, state, time.Now(), nodeops.NodeAnnotationConfig{}, nil)

	r := &controller.Reconciler{
		Client:            client,
		Cfg:               &config.Config{DryRun: true},
		State:             state,
		Metrics:           &mockMetrics{},
		Shutdowner:        &mockShutdowner{},
		ScaleDownStrategy: &mockScaleDownStrategy{},
	}

	result := r.MaybeScaleDown(context.Background(), []*nodeops.NodeWrapper{wrapped})
	if !result {
		t.Error("Expected dry-run scale down to return true")
	}
}

func TestUncordonNode_DryRun(t *testing.T) {
	client := fake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
		Spec:       v1.NodeSpec{Unschedulable: true},
	})

	r := &controller.Reconciler{
		Client: client,
		Cfg:    &config.Config{DryRun: true},
	}

	err := r.UncordonNode(context.Background(), "node1")
	if err != nil {
		t.Errorf("Expected no error during dry-run uncordon, got: %v", err)
	}
}
