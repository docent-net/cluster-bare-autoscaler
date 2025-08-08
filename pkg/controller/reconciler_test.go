package controller_test

import (
	"context"
	"fmt"
	policyv1 "k8s.io/api/policy/v1"
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
	type testCase struct {
		name   string
		dryRun bool
		expect []string
	}
	for _, tc := range []testCase{
		{name: "real run - should power on node", dryRun: false, expect: []string{"node1"}},
		{name: "dry run - should not power on node", dryRun: true, expect: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Labels: map[string]string{
						"scaling-managed-by-cba": "true",
					},
					Annotations: map[string]string{
						"cba.dev/mac-address": "00:11:22:33:44:55",
					},
				},
			})

			state := nodeops.NewNodeStateTracker()
			mockPower := &mockPowerOnController{}

			// Mock scale-up strategy: always returns "node1", true, nil
			strategy := &mockScaleUpStrategy{
				node:  "node1",
				ok:    true,
				cause: nil,
			}

			reconciler := &controller.Reconciler{
				Client: client,
				Cfg: &config.Config{
					DryRun: tc.dryRun,
					NodeLabels: config.NodeLabelConfig{
						Managed: "scaling-managed-by-cba",
					},
					NodeAnnotations: config.NodeAnnotationConfig{
						MAC: "cba.dev/mac-address",
					},
				},
				State:           state,
				PowerOner:       mockPower,
				ScaleUpStrategy: strategy,
			}

			ok := reconciler.MaybeScaleUp(context.Background())
			require.True(t, ok, "scale-up should return true in happy path")
			require.ElementsMatch(t, tc.expect, mockPower.PoweredOn)
		})
	}
}

// Mock scale-up strategy for testing
type mockScaleUpStrategy struct {
	node  string
	ok    bool
	cause error
}

func (m *mockScaleUpStrategy) ShouldScaleUp(ctx context.Context) (string, bool, error) {
	return m.node, m.ok, m.cause
}

func (m *mockScaleUpStrategy) Name() string { return "mock" }

func TestPickScaleDownCandidate(t *testing.T) {
	type scenario struct {
		name         string
		eligible     []string
		minNodes     int
		expectedNode string // empty string means expect nil
	}
	cases := []scenario{
		{
			name:         "multiple eligible, minNodes smaller — pick last",
			eligible:     []string{"node1", "node2", "node3"},
			minNodes:     2,
			expectedNode: "node3",
		},
		{
			name:         "minNodes equals eligible — returns nil",
			eligible:     []string{"node1", "node2"},
			minNodes:     2,
			expectedNode: "",
		},
		{
			name:         "minNodes greater than eligible — returns nil",
			eligible:     []string{"node1"},
			minNodes:     2,
			expectedNode: "",
		},
		{
			name:         "minNodes zero, many eligible — pick last",
			eligible:     []string{"nodeA", "nodeB"},
			minNodes:     0,
			expectedNode: "nodeB",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eligible := make([]*nodeops.NodeWrapper, 0, len(tc.eligible))
			for _, n := range tc.eligible {
				eligible = append(eligible, &nodeops.NodeWrapper{Node: &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: n}}})
			}
			reconciler := &controller.Reconciler{
				Cfg: &config.Config{MinNodes: tc.minNodes},
			}
			node := reconciler.PickScaleDownCandidate(eligible)
			if tc.expectedNode == "" {
				require.Nil(t, node)
			} else {
				require.NotNil(t, node)
				require.Equal(t, tc.expectedNode, node.Name)
			}
		})
	}

}

func TestCordonAndDrain_Success(t *testing.T) {
	type testCase struct {
		name        string
		dryRun      bool
		expectEvict bool
	}
	for _, tc := range []testCase{
		{name: "real run - evict pod", dryRun: false, expectEvict: true},
		{name: "dry run - do not evict", dryRun: true, expectEvict: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			nodeName := "node1"

			// Node to cordon/drain
			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: v1.NodeSpec{
					Unschedulable: false,
				},
			}

			// Normal pod (should be evicted)
			evictMe := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "evict-me",
					Namespace: "default",
					UID:       "evictme-uid",
				},
				Spec: v1.PodSpec{
					NodeName: nodeName,
				},
			}

			// Mirror pod (should NOT be evicted)
			mirrorPod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mirror-pod",
					Namespace: "default",
					UID:       "mirrorpod-uid",
					Annotations: map[string]string{
						"kubernetes.io/config.mirror": "true",
					},
				},
				Spec: v1.PodSpec{
					NodeName: nodeName,
				},
			}

			// DaemonSet pod (should NOT be evicted)
			dsPod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ds-pod",
					Namespace: "default",
					OwnerReferences: []metav1.OwnerReference{{
						Kind:       "DaemonSet",
						Name:       "ds-owner",
						Controller: func() *bool { b := true; return &b }(),
					}},
				},
				Spec: v1.PodSpec{
					NodeName: nodeName,
				},
			}

			client := fake.NewSimpleClientset(node, evictMe, mirrorPod, dsPod)

			var evictedPods []string
			client.Fake.PrependReactor("create", "pods/eviction", func(action k8stesting.Action) (bool, runtime.Object, error) {
				obj := action.(k8stesting.CreateAction).GetObject()
				if e, ok := obj.(*policyv1.Eviction); ok {
					evictedPods = append(evictedPods, e.Name)
					return true, nil, nil
				}
				return false, nil, nil
			})

			r := &controller.Reconciler{
				Client: client,
				Cfg:    &config.Config{DryRun: tc.dryRun},
			}

			nw := &nodeops.NodeWrapper{Node: node}
			err := r.CordonAndDrain(ctx, nw)
			require.NoError(t, err, "CordonAndDrain should succeed")

			updated, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
			require.NoError(t, err)
			if !tc.dryRun {
				require.True(t, updated.Spec.Unschedulable, "node should be unschedulable")
			}

			if tc.expectEvict {
				require.Contains(t, evictedPods, "evict-me", "evict-me pod should have been evicted")
				require.NotContains(t, evictedPods, "mirror-pod", "mirror pod should NOT be evicted (fix filtering logic if this fails!)")
				require.NotContains(t, evictedPods, "ds-pod", "daemonset pod should NOT be evicted (fix filtering logic if this fails!)")
			} else {
				require.Empty(t, evictedPods, "no pods should be evicted in dry-run mode")
			}
		})
	}
}

func TestRestorePoweredOffState(t *testing.T) {
	t.Skip("TODO: test detection of powered-off nodes from annotation and active list")
}

func TestAnnotatePoweredOffNode_PatchError(t *testing.T) {
	t.Skip("TODO: simulate patch failure and verify error is handled/logged")
}
