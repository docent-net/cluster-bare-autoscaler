package controller

import (
	"testing"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeNode(name string, labels map[string]string) v1.Node {
	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

func TestGetEligibleNodes(t *testing.T) {
	cfg := &config.Config{
		IgnoreLabels: map[string]string{
			"node-role.kubernetes.io/control-plane": "",
		},
	}
	r := &Reconciler{cfg: cfg}

	nodes := []v1.Node{
		makeNode("node1", map[string]string{}),
		makeNode("cp1", map[string]string{"node-role.kubernetes.io/control-plane": ""}),
		makeNode("node2", map[string]string{}),
	}

	eligible := r.getEligibleNodes(nodes)
	if len(eligible) != 2 {
		t.Errorf("expected 2 eligible nodes, got %d", len(eligible))
	}
}

func TestPickScaleDownCandidate(t *testing.T) {
	cfg := &config.Config{
		MinNodes: 2,
	}
	r := &Reconciler{cfg: cfg}

	nodes := []v1.Node{
		makeNode("node1", nil),
		makeNode("node2", nil),
		makeNode("node3", nil),
	}

	candidate := r.pickScaleDownCandidate(nodes)
	if candidate == nil || candidate.Name != "node3" {
		t.Errorf("expected node3 as candidate, got %v", candidate)
	}

	nodes = nodes[:2]
	candidate = r.pickScaleDownCandidate(nodes)
	if candidate != nil {
		t.Errorf("expected nil candidate when at or below MinNodes, got %v", candidate)
	}
}
