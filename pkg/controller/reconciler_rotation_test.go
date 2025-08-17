package controller_test

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/controller"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corefake "k8s.io/client-go/kubernetes/fake"
)

// --- helpers ---

func managedReady(name string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"scaling-managed-by-cba": "true"},
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{{Type: v1.NodeReady, Status: v1.ConditionTrue}},
		},
	}
}

type shutdownRecorder struct{ calls []string }

func (s *shutdownRecorder) Shutdown(_ context.Context, node string) error {
	s.calls = append(s.calls, node)
	return nil
}

// --- PickRotationPoweroffCandidate ---

func TestPickRotationPoweroffCandidate_DisabledDelegatesToPickScaleDown(t *testing.T) {
	cfg := &config.Config{
		MinNodes: 0,
		NodeLabels: config.NodeLabelConfig{
			Managed:  "scaling-managed-by-cba",
			Disabled: "cba.dev/disabled",
		},
		IgnoreLabels: map[string]string{},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{
			Enabled: false,
		},
	}
	r := &controller.Reconciler{Cfg: cfg}

	state := nodeops.NewNodeStateTracker()
	now := time.Now()
	wrappers := []*nodeops.NodeWrapper{
		nodeops.NewNodeWrapper(managedReady("n1"), state, now, nodeops.NodeAnnotationConfig{}, cfg.IgnoreLabels),
		nodeops.NewNodeWrapper(managedReady("n2"), state, now, nodeops.NodeAnnotationConfig{}, cfg.IgnoreLabels),
		nodeops.NewNodeWrapper(managedReady("n3"), state, now, nodeops.NodeAnnotationConfig{}, cfg.IgnoreLabels),
	}

	want := r.PickScaleDownCandidate(wrappers)
	got := r.PickRotationPoweroffCandidate(context.Background(), wrappers)

	if want == nil || got == nil || want.Name != got.Name {
		t.Fatalf("expected same candidate as PickScaleDownCandidate, want=%v got=%v", nn(want), nn(got))
	}
}

func TestPickRotationPoweroffCandidate_LoadAvg_AllowsOrBlocksByThresholds(t *testing.T) {
	cfg := &config.Config{
		NodeLabels: config.NodeLabelConfig{Managed: "scaling-managed-by-cba", Disabled: "cba.dev/disabled"},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{
			Enabled:            true,
			NodeThreshold:      0.5,
			ScaleDownThreshold: 0.6,
		},
	}
	state := nodeops.NewNodeStateTracker()
	wrapper := nodeops.NewNodeWrapper(managedReady("n1"), state, time.Now(), nodeops.NodeAnnotationConfig{}, nil)

	t.Run("allows when both below", func(t *testing.T) {
		node := 0.2
		cluster := 0.3
		r := &controller.Reconciler{Cfg: cfg, DryRunNodeLoad: &node, DryRunClusterLoadDown: &cluster}
		if cand := r.PickRotationPoweroffCandidate(context.Background(), []*nodeops.NodeWrapper{wrapper}); cand == nil {
			t.Fatal("expected a candidate when loads are below thresholds")
		}
	})
	t.Run("blocks when node too high", func(t *testing.T) {
		node := 0.7 // >= 0.5
		cluster := 0.3
		r := &controller.Reconciler{Cfg: cfg, DryRunNodeLoad: &node, DryRunClusterLoadDown: &cluster}
		if cand := r.PickRotationPoweroffCandidate(context.Background(), []*nodeops.NodeWrapper{wrapper}); cand != nil {
			t.Fatal("expected nil candidate when node load >= threshold")
		}
	})
	t.Run("blocks when cluster too high", func(t *testing.T) {
		node := 0.2
		cluster := 0.9 // >= 0.6
		r := &controller.Reconciler{Cfg: cfg, DryRunNodeLoad: &node, DryRunClusterLoadDown: &cluster}
		if cand := r.PickRotationPoweroffCandidate(context.Background(), []*nodeops.NodeWrapper{wrapper}); cand != nil {
			t.Fatal("expected nil candidate when cluster load >= threshold")
		}
	})
}

func TestMaybeRotate_PowersOffOne_WhenEligible(t *testing.T) {
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
	)

	cfg := &config.Config{
		DryRun:              true, // still calls Shutdowner
		MinNodes:            0,
		NodeLabels:          config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: "cba.dev/disabled"},
		NodeAnnotations:     config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:            config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{Enabled: false},
	}

	rec := &shutdownRecorder{}
	r := &controller.Reconciler{Cfg: cfg, Client: client, State: nodeops.NewNodeStateTracker(), Shutdowner: rec}
	r.MaybeRotate(context.Background())

	if len(rec.calls) != 1 {
		t.Fatalf("expected exactly one Shutdown call, got %v", rec.calls)
	}
}

func TestMaybeRotate_LoadAvg_GatesShutdown(t *testing.T) {
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
		managedNode("n3", true),
	)

	cfg := &config.Config{
		DryRun:          true,
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

	t.Run("allowed when low", func(t *testing.T) {
		node, cluster := 0.2, 0.3
		rec := &shutdownRecorder{}
		r := &controller.Reconciler{
			Cfg: cfg, Client: client, State: nodeops.NewNodeStateTracker(), Shutdowner: rec,
			DryRunNodeLoad: &node, DryRunClusterLoadDown: &cluster,
		}
		r.MaybeRotate(context.Background())
		if len(rec.calls) != 1 {
			t.Fatalf("expected a Shutdown when loads are low, got %v", rec.calls)
		}
	})

	t.Run("blocked when high", func(t *testing.T) {
		node, cluster := 0.9, 0.9
		rec := &shutdownRecorder{}
		r := &controller.Reconciler{
			Cfg: cfg, Client: client, State: nodeops.NewNodeStateTracker(), Shutdowner: rec,
			DryRunNodeLoad: &node, DryRunClusterLoadDown: &cluster,
		}
		r.MaybeRotate(context.Background())
		if len(rec.calls) != 0 {
			t.Fatalf("expected no Shutdown when loads are high, got %v", rec.calls)
		}
	})
}

func TestMaybeRotate_RealRun_AppliesAnnotationAndCordon(t *testing.T) {
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
		managedNode("n3", true),
	)

	cfg := &config.Config{
		DryRun:              false, // real path
		MinNodes:            0,
		NodeLabels:          config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: "cba.dev/disabled"},
		NodeAnnotations:     config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:            config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{Enabled: false},
	}

	rec := &shutdownRecorder{}
	mockPower := &mockPowerOnController{}
	r := &controller.Reconciler{
		Cfg:        cfg,
		Client:     client,
		State:      nodeops.NewNodeStateTracker(),
		Shutdowner: rec,
		PowerOner:  mockPower,
	}
	r.MaybeRotate(context.Background())

	if len(rec.calls) != 1 {
		t.Fatalf("expected exactly one Shutdown call, got %v", rec.calls)
	}

	// find the annotated+cordoned node
	nodes, err := client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	var annotated []string
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if n.Spec.Unschedulable && n.Annotations[nodeops.AnnotationPoweredOff] != "" {
			annotated = append(annotated, n.Name)
		}
	}
	if len(annotated) != 1 {
		t.Fatalf("expected exactly one node to be cordoned+annotated, got %v", annotated)
	}
	if annotated[0] != rec.calls[0] {
		t.Fatalf("annotation/cordon mismatch: annotated=%v shutdown=%v", annotated[0], rec.calls[0])
	}
}

func TestMaybeRotate_SkipsIgnoredAndDisabledCandidates(t *testing.T) {
	disabledKey := "cba.dev/disabled"
	ignoreKey := "ignore.me"

	overdue := poweredOffSince(managedNode("off-old", false), time.Now().Add(-3*time.Hour))
	nD := managedNode("nD", true)
	if nD.Labels == nil {
		nD.Labels = map[string]string{}
	}
	nD.Labels[disabledKey] = "true"
	nI := managedNode("nI", true)
	if nI.Labels == nil {
		nI.Labels = map[string]string{}
	}
	nI.Labels[ignoreKey] = "true"

	client := corefake.NewSimpleClientset(
		overdue,
		managedNode("n1", true),
		managedNode("n2", true),
		nD,
		nI,
	)

	cfg := &config.Config{
		DryRun:              true,
		MinNodes:            1, // two eligibles (n1,n2) -> can retire exactly one
		NodeLabels:          config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: disabledKey},
		NodeAnnotations:     config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:            config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute},
		IgnoreLabels:        map[string]string{ignoreKey: "true"},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{Enabled: false},
	}

	rec := &shutdownRecorder{}
	r := &controller.Reconciler{Cfg: cfg, Client: client, State: nodeops.NewNodeStateTracker(), Shutdowner: rec}
	r.MaybeRotate(context.Background())

	if len(rec.calls) != 1 {
		t.Fatalf("expected exactly one Shutdown call, got %v", rec.calls)
	}
	got := rec.calls[0]
	if got == "nD" || got == "nI" {
		t.Fatalf("disabled/ignored node must not be chosen; got %q", got)
	}
}

func TestMaybeRotate_RespectsMinNodes(t *testing.T) {
	client := corefake.NewSimpleClientset(managedReady("a"), managedReady("b"))

	cfg := &config.Config{
		DryRun:   true,
		MinNodes: 2, // eligible == minNodes → skip
		NodeLabels: config.NodeLabelConfig{
			Managed:  "scaling-managed-by-cba",
			Disabled: "cba.dev/disabled",
		},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{Enabled: false},
	}

	rec := &shutdownRecorder{}
	r := &controller.Reconciler{
		Cfg:        cfg,
		Client:     client,
		State:      nodeops.NewNodeStateTracker(),
		Shutdowner: rec,
	}

	r.MaybeRotate(context.Background())
	if len(rec.calls) != 0 {
		t.Fatalf("expected no Shutdown when eligible <= minNodes, got %v", rec.calls)
	}
}

// tiny helper for error messages
func nn(w *nodeops.NodeWrapper) string {
	if w == nil {
		return "<nil>"
	}
	return w.Name
}

// helper: mark node as powered-off since a given time
func poweredOffSince(node *v1.Node, since time.Time) *v1.Node {
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	// timestamp annotation used by rotation ordering
	node.Annotations[nodeops.AnnotationPoweredOff] = since.UTC().Format(time.RFC3339)
	// give it a MAC so PowerOn can work (unless test already set one)
	if _, ok := node.Annotations[nodeops.AnnotationMACAuto]; !ok {
		node.Annotations[nodeops.AnnotationMACAuto] = "00:11:22:33:44:55"
	}
	// simulate a shut-down node (unschedulable + NotReady)
	node.Spec.Unschedulable = true
	set := false
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == v1.NodeReady {
			node.Status.Conditions[i].Status = v1.ConditionFalse
			set = true
			break
		}
	}
	if !set {
		node.Status.Conditions = append(node.Status.Conditions, v1.NodeCondition{
			Type:   v1.NodeReady,
			Status: v1.ConditionFalse,
		})
	}
	return node
}

// helper: create a managed node with optional Ready status
func managedNode(name string, ready bool) *v1.Node {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      map[string]string{"cba.dev/is-managed": "true"},
			Annotations: map[string]string{},
		},
	}
	status := v1.ConditionFalse
	if ready {
		status = v1.ConditionTrue
	}
	n.Status.Conditions = []v1.NodeCondition{
		{Type: v1.NodeReady, Status: status},
	}
	return n
}
