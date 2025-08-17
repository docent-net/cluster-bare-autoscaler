package nodeops_test

import (
	"context"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corefake "k8s.io/client-go/kubernetes/fake"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
)

func TestListManagedNodes(t *testing.T) {
	ctx := context.Background()
	filter := nodeops.ManagedNodeFilter{
		ManagedLabel:  "cba.dev/is-managed",
		DisabledLabel: "cba.dev/disabled",
		IgnoreLabels: map[string]string{
			"node-role.kubernetes.io/control-plane": "",
			"node-home-assistant":                   "yes",
		},
	}

	t.Run("returns only eligible nodes", func(t *testing.T) {
		client := corefake.NewSimpleClientset(
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Labels: map[string]string{
						"cba.dev/is-managed": "true",
					},
				},
			},
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node2",
					Labels: map[string]string{
						"cba.dev/is-managed": "true",
						"cba.dev/disabled":   "true",
					},
				},
			},
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node3",
					Labels: map[string]string{
						"cba.dev/is-managed":                    "true",
						"node-role.kubernetes.io/control-plane": "",
					},
				},
			},
			&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node4",
					Labels: map[string]string{
						"cba.dev/is-managed":  "true",
						"node-home-assistant": "yes",
					},
				},
			},
		)

		nodes, err := nodeops.ListManagedNodes(ctx, client, filter)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(nodes) != 1 || nodes[0].Name != "node1" {
			t.Errorf("expected only node1, got: %+v", nodes)
		}
	})

	t.Run("skips if ManagedLabel missing", func(t *testing.T) {
		client := corefake.NewSimpleClientset(&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-x",
				Labels: map[string]string{},
			},
		})
		nodes, _ := nodeops.ListManagedNodes(ctx, client, filter)
		if len(nodes) != 0 {
			t.Errorf("expected no nodes, got: %+v", nodes)
		}
	})

	t.Run("skips if ManagedLabel is not 'true'", func(t *testing.T) {
		client := corefake.NewSimpleClientset(&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-y",
				Labels: map[string]string{"cba.dev/is-managed": "false"},
			},
		})
		nodes, _ := nodeops.ListManagedNodes(ctx, client, filter)
		if len(nodes) != 0 {
			t.Errorf("expected no nodes, got: %+v", nodes)
		}
	})

	t.Run("skips if DisabledLabel is true", func(t *testing.T) {
		client := corefake.NewSimpleClientset(&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-disabled",
				Labels: map[string]string{
					"cba.dev/is-managed": "true",
					"cba.dev/disabled":   "true",
				},
			},
		})
		nodes, _ := nodeops.ListManagedNodes(ctx, client, filter)
		if len(nodes) != 0 {
			t.Errorf("expected no nodes, got: %+v", nodes)
		}
	})

	t.Run("skips if IgnoreLabels key exists", func(t *testing.T) {
		client := corefake.NewSimpleClientset(&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cp-node",
				Labels: map[string]string{
					"cba.dev/is-managed":                    "true",
					"node-role.kubernetes.io/control-plane": "",
				},
			},
		})
		nodes, _ := nodeops.ListManagedNodes(ctx, client, filter)
		if len(nodes) != 0 {
			t.Errorf("expected no nodes, got: %+v", nodes)
		}
	})

	t.Run("accepts good node", func(t *testing.T) {
		client := corefake.NewSimpleClientset(&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "worker-node",
				Labels: map[string]string{
					"cba.dev/is-managed": "true",
				},
			},
		})
		nodes, _ := nodeops.ListManagedNodes(ctx, client, filter)
		if len(nodes) != 1 || nodes[0].Name != "worker-node" {
			t.Errorf("expected worker-node, got: %+v", nodes)
		}
	})
}

func TestListActiveNodes(t *testing.T) {
	tracker := nodeops.NewNodeStateTracker()
	client := corefake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-ok",
			Labels: map[string]string{
				"cba.dev/is-managed": "true",
			},
			Annotations: map[string]string{},
		},
		Spec: v1.NodeSpec{
			Unschedulable: false,
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{{
				Type:   v1.NodeReady,
				Status: v1.ConditionTrue,
			}},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-down",
			Labels: map[string]string{
				"cba.dev/is-managed": "true",
			},
			Annotations: map[string]string{
				nodeops.AnnotationPoweredOff: "true",
			},
		},
		Spec: v1.NodeSpec{
			Unschedulable: false,
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{{
				Type:   v1.NodeReady,
				Status: v1.ConditionTrue,
			}},
		},
	})

	result, err := nodeops.ListActiveNodes(context.Background(), client, tracker, nodeops.ManagedNodeFilter{
		ManagedLabel:  "cba.dev/is-managed",
		DisabledLabel: "cba.dev/disabled",
		IgnoreLabels:  map[string]string{},
	}, nodeops.ActiveNodeFilter{
		IgnoreLabels: map[string]string{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0].Name != "node-ok" {
		t.Errorf("expected only node-ok as active, got: %+v", result)
	}
}

func TestFilterShutdownEligibleNodes(t *testing.T) {
	now := time.Now()
	tracker := nodeops.NewNodeStateTracker()
	cfg := nodeops.EligibilityConfig{
		Cooldown:     10 * time.Minute,
		BootCooldown: 10 * time.Minute,
		IgnoreLabels: map[string]string{
			"ignore": "true",
		},
	}

	nodes := []v1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "ok",
				Labels:      map[string]string{},
				Annotations: map[string]string{},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "ignored",
				Labels: map[string]string{"ignore": "true"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cordoned",
			},
			Spec: v1.NodeSpec{Unschedulable: true},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cooling-down",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "booting",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "annotated-powered-off",
				Annotations: map[string]string{nodeops.AnnotationPoweredOff: "true"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tracked-powered-off",
			},
		},
	}

	tracker.MarkShutdown("cooling-down")
	tracker.SetShutdownTime("cooling-down", now.Add(-5*time.Minute))
	tracker.MarkBooted("booting")
	tracker.MarkPoweredOff("tracked-powered-off")

	eligible := nodeops.FilterShutdownEligibleNodes(nodes, tracker, now, cfg)
	if len(eligible) != 1 || eligible[0].Name != "ok" {
		t.Errorf("expected only 'ok' node to be eligible, got: %+v", eligible)
	}
}

func TestWrapNodes(t *testing.T) {
	nodes := []v1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "node-a",
				Annotations: map[string]string{nodeops.AnnotationMACManual: "11:22:33:44:55:66"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "node-b",
				Annotations: map[string]string{nodeops.AnnotationMACAuto: "aa:bb:cc:dd:ee:ff"},
			},
		},
	}

	wrapped := nodeops.WrapNodes(nodes, nodeops.NewNodeStateTracker(), time.Now(), nodeops.NodeAnnotationConfig{}, nil)

	if len(wrapped) != 2 {
		t.Fatalf("expected 2 wrapped nodes, got %d", len(wrapped))
	}
	if !wrapped[0].HasManualMACOverride() {
		t.Errorf("expected node-a to have manual MAC override")
	}
	if !wrapped[1].HasDiscoveredMACAddr() {
		t.Errorf("expected node-b to have discovered MAC address")
	}
}

func TestRecoverUnexpectedlyBootedNodes(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		node         *v1.Node
		shouldChange bool
	}{
		{
			name: "recovers a node with annotation and unschedulable",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "recover-node",
					Labels: map[string]string{
						"cba.dev/is-managed": "true",
					},
					Annotations: map[string]string{
						"cba.dev/was-powered-off": "true",
					},
				},
				Spec: v1.NodeSpec{
					Unschedulable: true,
				},
				Status: v1.NodeStatus{
					Conditions: []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
					},
				},
			},
			shouldChange: true,
		},
		{
			name: "ignores node without annotation",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "no-annotation",
					Labels: map[string]string{
						"cba.dev/is-managed": "true",
					},
				},
			},
			shouldChange: false,
		},
		{
			name: "ignores node if not cordoned",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "not-cordoned",
					Labels: map[string]string{
						"cba.dev/is-managed": "true",
					},
					Annotations: map[string]string{
						"cba.dev/was-powered-off": "true",
					},
				},
				Spec: v1.NodeSpec{
					Unschedulable: false,
				},
				Status: v1.NodeStatus{
					Conditions: []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
					},
				},
			},
			shouldChange: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := corefake.NewSimpleClientset(tt.node)

			cfg := &config.Config{
				NodeLabels: config.NodeLabelConfig{
					Managed:  "cba.dev/is-managed",
					Disabled: "cba.dev/is-disabled",
				},
				IgnoreLabels: map[string]string{},
			}

			err := nodeops.RecoverUnexpectedlyBootedNodes(ctx, client, cfg, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			updated, err := client.CoreV1().Nodes().Get(ctx, tt.node.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get node: %v", err)
			}

			_, hasAnnotation := updated.Annotations[nodeops.AnnotationPoweredOff]
			isCordoned := updated.Spec.Unschedulable

			if tt.shouldChange {
				if isCordoned {
					t.Errorf("expected node to be uncordoned")
				}
				if hasAnnotation {
					t.Errorf("expected powered-off annotation to be removed")
				}
			} else {
				if !isCordoned && tt.node.Spec.Unschedulable {
					t.Errorf("expected node to remain cordoned")
				}
				if !hasAnnotation && len(tt.node.Annotations) > 0 {
					t.Errorf("expected annotation to remain")
				}
			}
		})
	}
}

func TestIsNodeReady(t *testing.T) {
	tests := []struct {
		name     string
		node     v1.Node
		expected bool
	}{
		{
			name: "node is ready",
			node: v1.Node{
				Status: v1.NodeStatus{
					Conditions: []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "node is not ready (False)",
			node: v1.Node{
				Status: v1.NodeStatus{
					Conditions: []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionFalse,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "node is not ready (Unknown)",
			node: v1.Node{
				Status: v1.NodeStatus{
					Conditions: []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionUnknown,
						},
					},
				},
			},
			expected: false,
		},
		{
			name:     "node has no conditions",
			node:     v1.Node{},
			expected: false,
		},
		{
			name: "node has multiple conditions, including Ready=True",
			node: v1.Node{
				Status: v1.NodeStatus{
					Conditions: []v1.NodeCondition{
						{Type: v1.NodeMemoryPressure, Status: v1.ConditionFalse},
						{Type: v1.NodeReady, Status: v1.ConditionTrue},
					},
				},
			},
			expected: true,
		},
		{
			name: "node has multiple conditions, Ready=False",
			node: v1.Node{
				Status: v1.NodeStatus{
					Conditions: []v1.NodeCondition{
						{Type: v1.NodeDiskPressure, Status: v1.ConditionFalse},
						{Type: v1.NodeReady, Status: v1.ConditionFalse},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nodeops.IsNodeReady(&tt.node)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestShouldIgnoreNodeDueToLabels_PresenceOnly(t *testing.T) {
	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"node-role.kubernetes.io/control-plane": "",
			},
		},
	}
	// presence-only rule: value in rule is "", node has the key => should ignore
	rule := map[string]string{"node-role.kubernetes.io/control-plane": ""}

	if !nodeops.ShouldIgnoreNodeDueToLabels(node, rule) {
		t.Fatalf("expected node to be ignored by presence-only rule")
	}
}

func TestShouldIgnoreNodeDueToLabels_ValueMatch(t *testing.T) {
	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"node-home-assistant": "yes",
			},
		},
	}
	// exact value match should ignore
	if !nodeops.ShouldIgnoreNodeDueToLabels(node, map[string]string{"node-home-assistant": "yes"}) {
		t.Fatalf("expected node to be ignored by exact value match")
	}
	// value mismatch should NOT ignore
	if nodeops.ShouldIgnoreNodeDueToLabels(node, map[string]string{"node-home-assistant": "no"}) {
		t.Fatalf("did not expect node to be ignored when values differ")
	}
}

func TestRecoverUnexpectedlyBootedNodes_SkipsWhenIgnoredByLabels(t *testing.T) {
	ctx := context.Background()

	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ignored-cp-node",
			Labels: map[string]string{
				"cba.dev/is-managed":                    "true",
				"node-role.kubernetes.io/control-plane": "",
			},
			Annotations: map[string]string{
				"cba.dev/was-powered-off": "true",
			},
		},
		Spec: v1.NodeSpec{Unschedulable: true},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{{Type: v1.NodeReady, Status: v1.ConditionTrue}},
		},
	}

	client := corefake.NewSimpleClientset(n)
	cfg := &config.Config{
		NodeLabels: config.NodeLabelConfig{
			Managed:  "cba.dev/is-managed",
			Disabled: "cba.dev/disabled",
		},
		// presence-only ignore rule — should skip recovery
		IgnoreLabels: map[string]string{"node-role.kubernetes.io/control-plane": ""},
	}

	if err := nodeops.RecoverUnexpectedlyBootedNodes(ctx, client, cfg, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := client.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get node: %v", err)
	}
	if !updated.Spec.Unschedulable {
		t.Fatalf("expected node to remain cordoned because it is ignored")
	}
	if _, has := updated.Annotations[nodeops.AnnotationPoweredOff]; !has {
		t.Fatalf("expected powered-off annotation to remain because node is ignored")
	}
}
