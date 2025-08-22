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
	corefake "k8s.io/client-go/kubernetes/fake"
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

func TestRestorePoweredOffState_Variants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// shared node reactors: first List => active only; second+ => full managed set
	newClientWithReactor := func() *fake.Clientset {
		client := fake.NewSimpleClientset()
		var listCount int
		client.Fake.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
			listCount++
			if listCount == 1 {
				return true, &v1.NodeList{Items: []v1.Node{{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-active-managed",
						Labels: map[string]string{"scaling-managed-by-cba": "true"},
					},
				}}}, nil
			}
			return true, &v1.NodeList{Items: []v1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-active-managed", Labels: map[string]string{"scaling-managed-by-cba": "true"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-missing-managed", Labels: map[string]string{"scaling-managed-by-cba": "true"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-managed-disabled", Labels: map[string]string{"scaling-managed-by-cba": "true", "cba.dev/disabled": "true"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-ignored", Labels: map[string]string{"scaling-managed-by-cba": "true", "ignore.me": "true"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-unmanaged", Labels: map[string]string{"some-other": "true"}}},
			}}, nil
		})
		return client
	}

	cfg := &config.Config{
		NodeLabels:   config.NodeLabelConfig{Managed: "scaling-managed-by-cba", Disabled: "cba.dev/disabled"},
		IgnoreLabels: map[string]string{"ignore.me": "true"},
	}

	// shared assertion
	assertPoweredOff := func(t *testing.T, r *controller.Reconciler, client *fake.Clientset) {
		t.Helper()
		require.True(t, r.State.IsPoweredOff("node-missing-managed"))
		require.False(t, r.State.IsPoweredOff("node-active-managed"))
		require.False(t, r.State.IsPoweredOff("node-managed-disabled"))
		require.False(t, r.State.IsPoweredOff("node-ignored"))
		require.False(t, r.State.IsPoweredOff("node-unmanaged"))

		// cross-check via nodeops
		offNames, err := nodeops.ListShutdownNodeNames(ctx, client,
			nodeops.ManagedNodeFilter{
				ManagedLabel:  r.Cfg.NodeLabels.Managed,
				DisabledLabel: r.Cfg.NodeLabels.Disabled,
				IgnoreLabels:  r.Cfg.IgnoreLabels,
			},
			r.State,
		)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"node-missing-managed"}, offNames)
	}

	cases := []struct {
		name string
		runs int
		via  string // "direct" or "constructor"
	}{
		{"direct_once", 1, "direct"},
		{"constructor_twice_idempotent", 2, "constructor"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < tc.runs; i++ {
				client := newClientWithReactor()
				state := nodeops.NewNodeStateTracker()
				r := &controller.Reconciler{Client: client, Cfg: cfg, State: state}

				if tc.via == "direct" {
					r.RestorePoweredOffState(ctx)
				} else {
					// constructor path also leads to the same restored state
					r = controller.NewReconciler(cfg, client, nil)
				}
				assertPoweredOff(t, r, client)
			}
		})
	}
}

func TestAnnotatePoweredOffNode_PatchError(t *testing.T) {
	ctx := context.Background()

	// Start with a managed node present in the fake cluster.
	client := fake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"scaling-managed-by-cba": "true",
			},
		},
	})

	// Make PATCH /nodes fail.
	client.Fake.PrependReactor("patch", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated patch error")
	})

	state := nodeops.NewNodeStateTracker()
	wrapped := nodeops.NewNodeWrapper(
		&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
				Labels: map[string]string{
					"scaling-managed-by-cba": "true",
				},
			},
		},
		state,
		time.Now(),
		nodeops.NodeAnnotationConfig{},
		nil,
	)

	r := &controller.Reconciler{
		Client: client,
		Cfg:    &config.Config{DryRun: false},
		State:  state,
	}

	// Act: attempt to annotate; expect error propagated from PATCH.
	err := r.AnnotatePoweredOffNode(ctx, wrapped)
	require.Error(t, err, "AnnotatePoweredOffNode should return the patch error") // :contentReference[oaicite:0]{index=0}
	require.Contains(t, err.Error(), "simulated patch error")

	// The node should not have the powered-off annotation after a failed patch.
	got, getErr := client.CoreV1().Nodes().Get(ctx, "node1", metav1.GetOptions{})
	require.NoError(t, getErr)
	require.NotContains(t, got.Annotations, nodeops.AnnotationPoweredOff, "annotation must not be present after failed patch") // :contentReference[oaicite:1]{index=1}
}

// --- test-only helpers at package scope ---

// shutdownMock records Shutdown invocations.
type shutdownMock struct{ calls int }

func (m *shutdownMock) Shutdown(ctx context.Context, node string) error {
	m.calls++
	return nil
}

// alwaysAllowStrategy approves scale-down for the named candidate.
type alwaysAllowStrategy struct{ candidate string }

func (s *alwaysAllowStrategy) ShouldScaleDown(_ context.Context, node string) (bool, error) {
	return node == s.candidate, nil
}
func (s *alwaysAllowStrategy) Name() string { return "allow-all" }

// --- the actual test ---

func TestMaybeScaleDown_AnnotatePatchError_AllowsShutdownAndMarksState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Fake cluster with one managed node.
	client := fake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"scaling-managed-by-cba": "true",
			},
		},
		Spec: v1.NodeSpec{Unschedulable: false},
	})

	// Make PATCH /nodes fail to simulate annotate error.
	client.Fake.PrependReactor("patch", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated patch failure")
	})

	// Minimal config: labels used by tests.
	cfg := &config.Config{
		DryRun: false,
		NodeLabels: config.NodeLabelConfig{
			Managed:  "scaling-managed-by-cba",
			Disabled: "cba.dev/disabled",
		},
		MinNodes: 0,
	}

	sm := &shutdownMock{}
	state := nodeops.NewNodeStateTracker()

	r := &controller.Reconciler{
		Cfg:               cfg,
		Client:            client,
		State:             state,
		Shutdowner:        sm,
		Metrics:           &FakeMetrics{},
		ScaleDownStrategy: &alwaysAllowStrategy{candidate: "node1"},
	}

	// Build candidate wrapper; no pods so CordonAndDrain succeeds.
	nodeObj, err := client.CoreV1().Nodes().Get(ctx, "node1", metav1.GetOptions{})
	require.NoError(t, err)
	wrapped := nodeops.NewNodeWrapper(nodeObj, state, time.Now(), nodeops.NodeAnnotationConfig{}, cfg.IgnoreLabels)

	ok := r.MaybeScaleDown(ctx, []*nodeops.NodeWrapper{wrapped})
	require.True(t, ok, "scale-down should proceed even if annotation patch fails") // annotation error is warned-only. :contentReference[oaicite:1]{index=1}

	// Shutdown must have been called.
	require.Equal(t, 1, sm.calls, "Shutdown should be invoked despite annotate error") // :contentReference[oaicite:2]{index=2}

	// State marked powered off (because not DryRun).
	require.True(t, state.IsPoweredOff("node1"), "node should be marked powered-off in memory") // :contentReference[oaicite:3]{index=3}

	// Annotation should NOT exist since PATCH failed.
	got, err := client.CoreV1().Nodes().Get(ctx, "node1", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotContains(t, got.Annotations, nodeops.AnnotationPoweredOff, "annotation should be absent after failed patch")

	// Cross-check visible powered-off set via nodeops helper.
	offNames, err := nodeops.ListShutdownNodeNames(ctx, client,
		nodeops.ManagedNodeFilter{
			ManagedLabel:  cfg.NodeLabels.Managed,
			DisabledLabel: cfg.NodeLabels.Disabled,
			IgnoreLabels:  cfg.IgnoreLabels,
		},
		state,
	)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"node1"}, offNames)
}

func TestMaybeScaleDown_ShutdownError_ClearsAnnotation(t *testing.T) {
	t.Skip("TODO: Simulate successful AnnotatePoweredOffNode, then have Shutdown() return an error. Verify that ClearPoweredOffAnnotation is attempted and state is still marked powered-off.")
}

func TestShutdownNodeNames_ListError(t *testing.T) {
	t.Skip("TODO: Force node client.List() to return an error in shutdownNodeNames(). Verify it returns nil slice and handles error without panic.")
}

func TestListActiveNodes_FilteringMatrix(t *testing.T) {
	t.Skip("TODO: Create a set of nodes: cordoned, NotReady, ignored by label, powered-off (via annotation & via state), and one healthy managed node. Verify only the healthy one is returned by listActiveNodes().")
}

func TestReconcile_RecoversUnexpectedlyBootedNodes(t *testing.T) {
	t.Skip("TODO: Seed a Ready, cordoned managed node with powered-off annotation. Run Reconcile() and verify it uncordons and clears the annotation.")
}

func TestNewReconciler_StrategyWiring_WithLoadAverage(t *testing.T) {
	t.Skip("TODO: Set LoadAverageStrategy.Enabled=true in config. Build reconciler with NewReconciler() and verify scale-down chain includes ResourceAware + LoadAverage, and scale-up chain includes MinNodeCount + LoadAverage with dry-run overrides.")
}

func Test_buildAggregateExclusions_MergesDisabledAndUserMap(t *testing.T) {
	cfg := &config.Config{
		NodeLabels: config.NodeLabelConfig{
			Disabled: "cba.dev/disabled",
		},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{
			ExcludeFromAggregateLabels: map[string]string{
				"node-role.kubernetes.io/control-plane": "",
				"kubernetes.io/hostname":                "cp-1",
			},
		},
	}

	got := controller.BuildAggregateExclusions(cfg)

	// disabled is always included with value "true"
	if v, ok := got["cba.dev/disabled"]; !ok || v != "true" {
		t.Fatalf("expected disabled label with value 'true', got %q (present=%v)", v, ok)
	}
	// presence-only key preserved (empty string value)
	if v, ok := got["node-role.kubernetes.io/control-plane"]; !ok || v != "" {
		t.Fatalf("expected presence-only control-plane key with empty value, got %q (present=%v)", v, ok)
	}
	// exact-value pair preserved
	if v := got["kubernetes.io/hostname"]; v != "cp-1" {
		t.Fatalf("expected hostname=cp-1, got %q", v)
	}
}

func Test_buildAggregateExclusions_NoDisabledConfigured(t *testing.T) {
	cfg := &config.Config{
		NodeLabels: config.NodeLabelConfig{
			Disabled: "",
		},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{
			ExcludeFromAggregateLabels: map[string]string{
				"scheduler.alpha.kubernetes.io/critical-pod": "",
			},
		},
	}

	got := controller.BuildAggregateExclusions(cfg)

	if _, ok := got["cba.dev/disabled"]; ok {
		t.Fatalf("did not expect disabled key when cfg.NodeLabels.Disabled is empty")
	}
	if v, ok := got["scheduler.alpha.kubernetes.io/critical-pod"]; !ok || v != "" {
		t.Fatalf("expected presence-only critical-pod label, got %q (present=%v)", v, ok)
	}
}

func TestMaybeRotate_Skips_WhenEligiblePlusOneAtOrBelowMinNodes(t *testing.T) {
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
	)
	cfg := &config.Config{
		DryRun:          false,
		MinNodes:        3, // eligible is 2; 2+1 == 3 -> skip
		NodeLabels:      config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: "cba.dev/disabled"},
		NodeAnnotations: config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:        config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute},
	}
	sh := &shutdownRecorder{}
	power := &mockPowerOnController{}
	r := &controller.Reconciler{Cfg: cfg, Client: client, State: nodeops.NewNodeStateTracker(), Shutdowner: sh, PowerOner: power}

	r.MaybeRotate(context.Background())

	if len(power.PoweredOn) != 0 {
		t.Fatalf("expected no power-on when eligible+1 <= minNodes, got %v", power.PoweredOn)
	}
	if len(sh.calls) != 0 {
		t.Fatalf("expected no shutdown, got %v", sh.calls)
	}
}

func TestMaybeRotate_Skips_OverdueNode_WithExemptLabel(t *testing.T) {
	overdue := poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour))
	if overdue.Labels == nil {
		overdue.Labels = map[string]string{}
	}
	overdue.Labels["cba.dev/rotation-exempt"] = "true"

	client := corefake.NewSimpleClientset(
		overdue,
		managedNode("n1", true),
		managedNode("n2", true),
	)
	cfg := &config.Config{
		DryRun:          false,
		MinNodes:        0,
		NodeLabels:      config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: "cba.dev/disabled"},
		NodeAnnotations: config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:        config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute, ExemptLabel: "cba.dev/rotation-exempt"},
	}
	sh := &shutdownRecorder{}
	power := &mockPowerOnController{}
	r := &controller.Reconciler{Cfg: cfg, Client: client, State: nodeops.NewNodeStateTracker(), Shutdowner: sh, PowerOner: power}

	r.MaybeRotate(context.Background())

	if len(power.PoweredOn) != 0 {
		t.Fatalf("expected no power-on when overdue is exempt, got %v", power.PoweredOn)
	}
}

func TestMaybeRotate_Skips_WhenNoTentativeCandidateDueToLoad(t *testing.T) {
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
	)
	cfg := &config.Config{
		DryRun:          false,
		MinNodes:        0,
		NodeLabels:      config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: "cba.dev/disabled"},
		NodeAnnotations: config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:        config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{
			Enabled:            true,
			NodeThreshold:      0.5,
			ScaleDownThreshold: 0.6,
		},
	}
	node, cluster := 0.9, 0.9 // too high -> PickRotationPoweroffCandidate returns nil
	sh := &shutdownRecorder{}
	power := &mockPowerOnController{}
	r := &controller.Reconciler{
		Cfg: cfg, Client: client, State: nodeops.NewNodeStateTracker(),
		Shutdowner: sh, PowerOner: power,
		DryRunNodeLoad: &node, DryRunClusterLoadDown: &cluster,
	}

	r.MaybeRotate(context.Background())

	if len(power.PoweredOn) != 0 {
		t.Fatalf("expected no power-on when no tentative retire candidate, got %v", power.PoweredOn)
	}
	if len(sh.calls) != 0 {
		t.Fatalf("expected no shutdown, got %v", sh.calls)
	}
}
