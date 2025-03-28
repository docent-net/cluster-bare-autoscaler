package controller

import (
	"testing"
	"time"

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

func TestGetEligibleNodes_Shuffling(t *testing.T) {
	r := &Reconciler{
		cfg:   mockConfig(),
		state: NewNodeStateTracker(),
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
		eligible := r.getEligibleNodes(nodes)
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
	r := &Reconciler{
		cfg:   cfg,
		state: NewNodeStateTracker(),
	}

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
	r := &Reconciler{
		cfg:   cfg,
		state: NewNodeStateTracker(),
	}

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

func TestCooldownExclusion(t *testing.T) {
	cfg := &config.Config{
		Cooldown: 5 * time.Minute,
	}
	state := NewNodeStateTracker()
	r := &Reconciler{cfg: cfg, state: state}

	node := makeNode("node1", nil)
	state.MarkShutdown("node1")
	state.recentlyShutdown["node1"] = time.Now().Add(-1 * time.Minute)

	nodes := []v1.Node{node}
	eligible := r.getEligibleNodes(nodes)
	if len(eligible) != 0 {
		t.Errorf("expected 0 eligible nodes due to cooldown, got %d", len(eligible))
	}
}

func TestPoweredOffNodeIsExcluded(t *testing.T) {
	r := &Reconciler{
		cfg:   &config.Config{},
		state: NewNodeStateTracker(),
	}
	node := makeNode("node2", nil)
	r.state.MarkPoweredOff("node2")

	nodes := []v1.Node{node}
	eligible := r.getEligibleNodes(nodes)
	if len(eligible) != 0 {
		t.Errorf("expected 0 eligible nodes due to powered-off status, got %d", len(eligible))
	}
}
