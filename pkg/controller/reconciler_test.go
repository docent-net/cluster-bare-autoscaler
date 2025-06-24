package controller_test

import (
	"context"
	"testing"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/controller"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Helpers

type FakeMetrics struct{}

func (f *FakeMetrics) RecordEligibleNodes(int) {}
func (f *FakeMetrics) RecordChosenNode(string) {}

type MockScaleDownStrategy struct {
	Candidate string
	Allow     bool
}

func (m *MockScaleDownStrategy) ShouldScaleDown(_ context.Context, node string) (bool, error) {
	if node == m.Candidate {
		return m.Allow, nil
	}
	return false, nil
}
func (m *MockScaleDownStrategy) Name() string { return "mock" }

// Test: Strategy denies scale-down decision
func TestMaybeScaleDown_StrategyDenies(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-deny",
			Labels: map[string]string{
				"scaling-managed-by-cba": "true",
			},
		},
	}
	_, _ = client.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})

	state := nodeops.NewNodeStateTracker()
	cfg := &config.Config{
		NodeLabels: config.NodeLabelConfig{
			Managed: "scaling-managed-by-cba",
		},
	}

	reconciler := &controller.Reconciler{
		Client: client,
		Cfg: &config.Config{
			DryRun: true,
			NodeLabels: config.NodeLabelConfig{
				Managed: "scaling-managed-by-cba",
			},
		},
		State:   state,
		Metrics: &FakeMetrics{},
		ScaleDownStrategy: &MockScaleDownStrategy{
			Candidate: "node-deny",
			Allow:     false,
		},
	}

	nodes, _ := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	wrappers := nodeops.WrapNodes(nodes.Items, state, time.Now(), nodeops.NodeAnnotationConfig(cfg.NodeAnnotations), cfg.IgnoreLabels)

	ok := reconciler.MaybeScaleDown(ctx, wrappers)
	require.False(t, ok)
}

func TestCordonAndDrain_EvictionFails(t *testing.T) {
	t.Skip("TODO: implement TestCordonAndDrain_EvictionFails logic")
}

func TestCordonAndDrain_SkipsMirrorAndDaemonSet(t *testing.T) {
	t.Skip("TODO: implement DaemonSet/mirror pod skip logic")
}

func TestMaybeScaleUp_Success(t *testing.T) {
	t.Skip("TODO: implement successful scale-up with node power-on")
}

func TestMaybeScaleUp_StrategyError(t *testing.T) {
	t.Skip("TODO: strategy error should be handled gracefully")
}

func TestAnnotatePoweredOffNode_DryRun(t *testing.T) {
	t.Skip("TODO: dry-run should skip powered-off annotation")
}

func TestAnnotatePoweredOffNode_EmptyKey(t *testing.T) {
	t.Skip("TODO: if annotation key is empty, should be no-op")
}

func TestReconcile_ForcePowerOnAllNodes(t *testing.T) {
	t.Skip("TODO: simulate full forced power-on path")
}

func TestReconcile_GlobalCooldownActive(t *testing.T) {
	t.Skip("TODO: skip reconcile if global cooldown active")
}

func TestReconcile_ForcePowerOnAllNodes_DryRun(t *testing.T) {
	t.Skip("TODO: dry-run should skip power-on even when force flag is set")
}
