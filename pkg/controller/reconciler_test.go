package controller_test

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
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
	ctx := context.Background()

	client := fake.NewSimpleClientset(
		&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
			},
			Spec: v1.NodeSpec{
				Unschedulable: false,
			},
		},
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mypod",
				Namespace: "default",
			},
			Spec: v1.PodSpec{
				NodeName: "node1",
			},
		},
	)

	// Simulate eviction failure
	client.Fake.PrependReactor("create", "pods/eviction", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("eviction failed")
	})

	r := &controller.Reconciler{
		Client: client,
		Cfg:    &config.Config{},
	}

	now := time.Now()
	state := nodeops.NewNodeStateTracker()
	wrapped := nodeops.NewNodeWrapper(
		&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
			},
			Spec: v1.NodeSpec{
				Unschedulable: false,
			},
		},
		state,
		now,
		nodeops.NodeAnnotationConfig{},
		map[string]string{},
	)

	err := r.CordonAndDrain(ctx, wrapped)
	require.Error(t, err)
	require.Contains(t, err.Error(), "aborting drain due to eviction failure")
}

func TestCordonAndDrain_SkipsMirrorAndDaemonSet(t *testing.T) {
	ctx := context.Background()

	client := fake.NewSimpleClientset(
		&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
			},
			Spec: v1.NodeSpec{
				Unschedulable: false,
			},
		},
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mirror-pod",
				Namespace: "default",
				Annotations: map[string]string{
					"kubernetes.io/config.mirror": "abc123",
				},
			},
			Spec: v1.PodSpec{
				NodeName: "node1",
			},
		},
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ds-pod",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{{
					Kind: "DaemonSet",
					Name: "ds-owner",
				}},
			},
			Spec: v1.PodSpec{
				NodeName: "node1",
			},
		},
	)

	r := &controller.Reconciler{
		Client: client,
		Cfg:    &config.Config{},
	}

	now := time.Now()
	state := nodeops.NewNodeStateTracker()
	wrapped := nodeops.NewNodeWrapper(
		&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
			},
			Spec: v1.NodeSpec{
				Unschedulable: false,
			},
		},
		state,
		now,
		nodeops.NodeAnnotationConfig{},
		map[string]string{},
	)

	err := r.CordonAndDrain(ctx, wrapped)
	require.NoError(t, err, "expected no error when draining node with mirror and DaemonSet pods")
}

func TestMaybeScaleUp_Success(t *testing.T) {
	t.Skip("TODO: implement successful scale-up with node power-on")
}

type failingScaleUpStrategy struct{}

func (f *failingScaleUpStrategy) ShouldScaleUp(_ context.Context) (string, bool, error) {
	return "", false, fmt.Errorf("simulated strategy error")
}

func (f *failingScaleUpStrategy) Name() string {
	return "failing-mock"
}

func TestMaybeScaleUp_StrategyError(t *testing.T) {
	ctx := context.Background()

	client := fake.NewSimpleClientset()

	reconciler := &controller.Reconciler{
		Client:          client,
		Cfg:             &config.Config{},
		State:           nodeops.NewNodeStateTracker(),
		ScaleUpStrategy: &failingScaleUpStrategy{},
	}

	ok := reconciler.MaybeScaleUp(ctx)
	require.False(t, ok, "MaybeScaleUp should return false on strategy error")
}

func TestAnnotatePoweredOffNode_DryRun(t *testing.T) {
	ctx := context.Background()

	client := fake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"scaling-managed-by-cba": "true",
			},
		},
	})

	state := nodeops.NewNodeStateTracker()
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"scaling-managed-by-cba": "true",
			},
		},
	}
	wrapped := nodeops.NewNodeWrapper(node, state, time.Now(), nodeops.NodeAnnotationConfig{}, nil)

	reconciler := &controller.Reconciler{
		Client: client,
		Cfg: &config.Config{
			DryRun: true,
		},
		State: state,
	}

	err := reconciler.AnnotatePoweredOffNode(ctx, wrapped)
	require.NoError(t, err, "AnnotatePoweredOffNode should return nil in dry-run mode")
}

func TestAnnotatePoweredOffNode_Success(t *testing.T) {
	ctx := context.Background()

	client := fake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"scaling-managed-by-cba": "true",
			},
		},
	})

	state := nodeops.NewNodeStateTracker()
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"scaling-managed-by-cba": "true",
			},
		},
	}
	wrapped := nodeops.NewNodeWrapper(node, state, time.Now(), nodeops.NodeAnnotationConfig{}, nil)

	reconciler := &controller.Reconciler{
		Client: client,
		Cfg:    &config.Config{DryRun: false},
		State:  state,
	}

	err := reconciler.AnnotatePoweredOffNode(ctx, wrapped)
	require.NoError(t, err, "annotatePoweredOffNode should succeed")

	// Verify annotation was applied
	updated, err := client.CoreV1().Nodes().Get(ctx, "node1", metav1.GetOptions{})
	require.NoError(t, err)
	require.Contains(t, updated.Annotations, nodeops.AnnotationPoweredOff)
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
