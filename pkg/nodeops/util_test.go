package nodeops_test

import (
	"context"
	"errors"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
)

type mockPower struct {
	called  bool
	fail    bool
	lastMAC string
}

func (m *mockPower) PowerOn(ctx context.Context, node, mac string) error {
	m.called = true
	m.lastMAC = mac
	if m.fail {
		return errors.New("mock failure")
	}
	return nil
}

func TestForcePowerOnAllNodes_DryRunSkips(t *testing.T) {
	client := corefake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node1",
			Labels: map[string]string{"scaling-managed-by-cba": "true"},
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{Type: v1.NodeReady, Status: v1.ConditionFalse},
			},
		},
	})
	cfg := &config.Config{
		DryRun: true,
		NodeLabels: config.NodeLabelConfig{
			Managed:  "scaling-managed-by-cba",
			Disabled: "scaling-disabled",
		},
		NodeAnnotations: config.NodeAnnotationConfig{
			MAC: "cba.dev/mac",
		},
	}
	state := nodeops.NewNodeStateTracker()
	powerMock := &mockPower{}

	err := nodeops.ForcePowerOnAllNodes(context.Background(), client, cfg, state, powerMock, true)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if powerMock.called {
		t.Errorf("power should not have been called in dry-run")
	}
}

func TestPowerOnAndMarkBooted_HandlesPowerFailure(t *testing.T) {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node2",
			Labels: map[string]string{},
			Annotations: map[string]string{
				"cba.dev/mac": "00:11:22:33:44:55",
			},
		},
	}
	state := nodeops.NewNodeStateTracker()
	mockPower := &mockPower{fail: true}
	cfg := &config.Config{
		NodeAnnotations: config.NodeAnnotationConfig{
			MAC: "cba.dev/mac",
		},
		DryRun: false,
	}
	annotations := nodeops.NodeAnnotationConfig{
		MAC: "cba.dev/mac",
	}

	err := nodeops.PowerOnAndMarkBooted(context.Background(), nodeops.NewNodeWrapper(node, state, time.Now(), annotations, nil), cfg, corefake.NewSimpleClientset(node), nil, mockPower, state, false)
	if err == nil || !mockPower.called {
		t.Errorf("expected failure and PowerOn call")
	}
}

func TestClearPoweredOffAnnotation_Success(t *testing.T) {
	client := corefake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node1",
			Annotations: map[string]string{"cba.dev/powered-off": "true"},
		},
	})
	err := nodeops.ClearPoweredOffAnnotation(context.Background(), client, "node1")
	if err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

func TestClearPoweredOffAnnotation_Failure(t *testing.T) {
	client := corefake.NewSimpleClientset()
	client.Fake.PrependReactor("patch", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("simulated patch failure")
	})
	err := nodeops.ClearPoweredOffAnnotation(context.Background(), client, "nodeX")
	if err == nil {
		t.Errorf("expected error from patch failure")
	}
}

func TestUncordonNode_Success(t *testing.T) {
	client := corefake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
		Spec:       v1.NodeSpec{Unschedulable: true},
	})
	err := nodeops.UncordonNode(context.Background(), client, "node1")
	if err != nil {
		t.Errorf("expected uncordon to succeed, got: %v", err)
	}
}

func TestUncordonNode_NoOp(t *testing.T) {
	client := corefake.NewSimpleClientset(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node2"},
		Spec:       v1.NodeSpec{Unschedulable: false},
	})
	err := nodeops.UncordonNode(context.Background(), client, "node2")
	if err != nil {
		t.Errorf("expected no-op success, got: %v", err)
	}
}

func TestFindPodIPOnNode_Found(t *testing.T) {
	client := corefake.NewSimpleClientset()
    _, _ = client.CoreV1().Pods("default").Create(context.Background(), &v1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "agent",
            Namespace: "default",
            Labels:    map[string]string{"app": "agent"},
        },
        Spec: v1.PodSpec{
            NodeName: "node1",
        },
        Status: v1.PodStatus{
            PodIP: "1.2.3.4",
        },
    }, metav1.CreateOptions{})
	ip, err := nodeops.FindPodIPOnNode(context.Background(), client, "default", "app=agent", "node1")
	if err != nil || ip != "1.2.3.4" {
		t.Errorf("expected pod IP 1.2.3.4, got %s (err: %v)", ip, err)
	}
}
