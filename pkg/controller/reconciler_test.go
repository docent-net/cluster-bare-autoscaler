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

type noopShutdownController struct{}

func (n *noopShutdownController) SendShutdownRequest(ctx context.Context, addr string, nodeName string) error {
	return nil
}

func (n *noopShutdownController) Shutdown(ctx context.Context, nodeName string) error {
	return nil
}

type mockPowerOnController struct {
	PoweredOn []string
}

func (m *mockPowerOnController) PowerOn(ctx context.Context, nodeName string, mac string) error {
	m.PoweredOn = append(m.PoweredOn, nodeName)
	return nil
}

func TestReconcile_ForcePowerOnAllNodes(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	// Define a shared set of nodes
	baseNodes := func() []runtime.Object {
		return []runtime.Object{
			// Should be powered on ✅
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Labels: map[string]string{
						"scaling-managed-by-cba": "true",
					},
					Annotations: map[string]string{
						nodeops.AnnotationPoweredOff: now.Add(-2 * time.Hour).Format(time.RFC3339),
						"cba.dev/mac-address":        "00:11:22:33:44:55",
					},
				},
				Spec: v1.NodeSpec{Unschedulable: true},
			},
			// Already on – should be skipped
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node2",
					Labels: map[string]string{
						"scaling-managed-by-cba": "true",
					},
				},
				Spec: v1.NodeSpec{Unschedulable: false},
			},
			// Not managed – should be skipped
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node3",
					Labels: map[string]string{
						"unrelated-label": "true",
					},
					Annotations: map[string]string{
						nodeops.AnnotationPoweredOff: now.Add(-1 * time.Hour).Format(time.RFC3339),
						"cba.dev/mac-address":        "00:22:33:44:55:66",
					},
				},
				Spec: v1.NodeSpec{Unschedulable: true},
			},
			// Missing MAC – should be skipped
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node4",
					Labels: map[string]string{
						"scaling-managed-by-cba": "true",
					},
					Annotations: map[string]string{
						nodeops.AnnotationPoweredOff: now.Add(-1 * time.Hour).Format(time.RFC3339),
					},
				},
				Spec: v1.NodeSpec{Unschedulable: true},
			},
		}
	}

	tests := []struct {
		name           string
		dryRun         bool
		expectedCalled []string
	}{
		{
			name:           "real run - powered off nodes should be powered on",
			dryRun:         false,
			expectedCalled: []string{"node1"},
		},
		{
			name:           "dry run - no nodes should be powered on",
			dryRun:         true,
			expectedCalled: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(baseNodes()...)

			state := nodeops.NewNodeStateTracker()
			state.MarkShutdown("node1")
			state.MarkShutdown("node3")
			state.MarkShutdown("node4")

			mockPower := &mockPowerOnController{}

			reconciler := &controller.Reconciler{
				Client: client,
				Cfg: &config.Config{
					ForcePowerOnAllNodes: true,
					DryRun:               tt.dryRun,
					NodeAnnotations: config.NodeAnnotationConfig{
						MAC: "cba.dev/mac-address",
					},
					NodeLabels: config.NodeLabelConfig{
						Managed: "scaling-managed-by-cba",
					},
				},
				State:      state,
				PowerOner:  mockPower,
				Shutdowner: &noopShutdownController{},
			}

			err := reconciler.Reconcile(ctx)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expectedCalled, mockPower.PoweredOn)
		})
	}
}

func TestReconcile_GlobalCooldown(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name            string
		lastShutdownAgo time.Duration
		cooldown        time.Duration
		dryRun          bool
		expectSkipped   bool
	}{
		{
			name:            "cooldown active - real run",
			lastShutdownAgo: 30 * time.Second,
			cooldown:        1 * time.Minute,
			dryRun:          false,
			expectSkipped:   false,
		},
		{
			name:            "cooldown expired - real run",
			lastShutdownAgo: 2 * time.Minute,
			cooldown:        1 * time.Minute,
			dryRun:          false,
			expectSkipped:   false,
		},
		{
			name:            "cooldown active - dry run",
			lastShutdownAgo: 30 * time.Second,
			cooldown:        1 * time.Minute,
			dryRun:          true,
			expectSkipped:   true,
		},
		{
			name:            "cooldown expired - dry run",
			lastShutdownAgo: 2 * time.Minute,
			cooldown:        1 * time.Minute,
			dryRun:          true,
			expectSkipped:   true, // dry-run disables actual power-on even if allowed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			client := fake.NewSimpleClientset(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Labels: map[string]string{
						"scaling-managed-by-cba": "true",
					},
					Annotations: map[string]string{
						nodeops.AnnotationPoweredOff: now.Add(-2 * time.Hour).Format(time.RFC3339),
						"cba.dev/mac-address":        "00:11:22:33:44:55",
					},
				},
				Spec: v1.NodeSpec{
					Unschedulable: true,
				},
			})

			state := nodeops.NewNodeStateTracker()
			state.LastShutdownTime = now.Add(-tt.lastShutdownAgo)
			state.MarkShutdown("node1")

			mockPower := &mockPowerOnController{}

			reconciler := &controller.Reconciler{
				Client: client,
				Cfg: &config.Config{
					Cooldown:             tt.cooldown,
					DryRun:               tt.dryRun,
					ForcePowerOnAllNodes: true,
					NodeAnnotations: config.NodeAnnotationConfig{
						MAC: "cba.dev/mac-address",
					},
					NodeLabels: config.NodeLabelConfig{
						Managed: "scaling-managed-by-cba",
					},
				},
				State:      state,
				PowerOner:  mockPower,
				Shutdowner: &noopShutdownController{},
			}

			err := reconciler.Reconcile(ctx)
			require.NoError(t, err)

			if tt.expectSkipped {
				require.Empty(t, mockPower.PoweredOn, "no nodes should be powered on in this case")
			} else {
				require.Equal(t, []string{"node1"}, mockPower.PoweredOn, "node1 should have been powered on")
			}
		})
	}
}

func TestReconcile_ForcePowerOnAllNodes_DryRun(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name          string
		dryRun        bool
		expectPowerOn bool
	}{
		{
			name:          "force power on - real run",
			dryRun:        false,
			expectPowerOn: true,
		},
		{
			name:          "force power on - dry run",
			dryRun:        true,
			expectPowerOn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			client := fake.NewSimpleClientset(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Labels: map[string]string{
						"scaling-managed-by-cba": "true",
					},
					Annotations: map[string]string{
						nodeops.AnnotationPoweredOff: now.Add(-1 * time.Hour).Format(time.RFC3339),
						"cba.dev/mac-address":        "00:11:22:33:44:55",
					},
				},
				Spec: v1.NodeSpec{
					Unschedulable: true,
				},
			})

			state := nodeops.NewNodeStateTracker()
			state.MarkShutdown("node1")

			mockPower := &mockPowerOnController{}

			reconciler := &controller.Reconciler{
				Client: client,
				Cfg: &config.Config{
					DryRun:               tt.dryRun,
					ForcePowerOnAllNodes: true,
					NodeAnnotations: config.NodeAnnotationConfig{
						MAC: "cba.dev/mac-address",
					},
					NodeLabels: config.NodeLabelConfig{
						Managed: "scaling-managed-by-cba",
					},
				},
				State:      state,
				PowerOner:  mockPower,
				Shutdowner: &noopShutdownController{},
			}

			err := reconciler.Reconcile(ctx)
			require.NoError(t, err)

			if tt.expectPowerOn {
				require.Equal(t, []string{"node1"}, mockPower.PoweredOn, "node should have been powered on")
			} else {
				require.Empty(t, mockPower.PoweredOn, "dry-run should skip actual power-on")
			}
		})
	}
}

func TestMaybeScaleUp_Success(t *testing.T) {
	t.Skip("TODO: implement successful scale-up with node power-on")
}

func TestPickScaleDownCandidate(t *testing.T) {
	t.Skip("TODO: test candidate selection logic based on minNodes and eligible list")
}

func TestCordonAndDrain_Success(t *testing.T) {
	t.Skip("TODO: implement happy path for successful eviction and drain")
}

func TestRestorePoweredOffState(t *testing.T) {
	t.Skip("TODO: test detection of powered-off nodes from annotation and active list")
}

func TestAnnotatePoweredOffNode_PatchError(t *testing.T) {
	t.Skip("TODO: simulate patch failure and verify error is handled/logged")
}
