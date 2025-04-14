package nodeops_test

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corefake "k8s.io/client-go/kubernetes/fake"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
)

func TestListManagedNodes(t *testing.T) {
	client := corefake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-a",
			Labels: map[string]string{
				"cba.dev/is-managed": "true",
			},
		},
	}, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-b",
			Labels: map[string]string{
				"cba.dev/is-managed": "false",
			},
		},
	})

	nodes, err := nodeops.ListManagedNodes(context.Background(), client, nodeops.ManagedNodeFilter{
		ManagedLabel:  "cba.dev/is-managed",
		DisabledLabel: "cba.dev/disabled",
		IgnoreLabels:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "node-a" {
		t.Errorf("expected node-a, got: %+v", nodes)
	}
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
