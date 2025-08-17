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

// replace the whole TestMaybeRotate_PowersOffOne_WhenEligible with this version
func TestMaybeRotate_PowersOffOne_WhenEligible(t *testing.T) {
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
	)

	cfg := &config.Config{
		DryRun:   false, // real power-on path (clears annotation & uncordons)
		MinNodes: 0,
		NodeLabels: config.NodeLabelConfig{
			Managed:  "cba.dev/is-managed",
			Disabled: "cba.dev/disabled",
		},
		NodeAnnotations: config.NodeAnnotationConfig{
			MAC: nodeops.AnnotationMACAuto,
		},
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

	// First rotation loop: should only power on the overdue node, no shutdown yet.
	r.MaybeRotate(context.Background())

	if len(rec.calls) != 0 {
		t.Fatalf("no shutdown should occur in the same loop as power-on, got %v", rec.calls)
	}
	// Verify we powered on the overdue node.
	found := false
	for _, n := range mockPower.PoweredOn {
		if n == "off-old" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected power-on of 'off-old', got %v", mockPower.PoweredOn)
	}
}

func TestMaybeRotate_LoadAvg_GatesShutdown(t *testing.T) {
	// Now: when loads are LOW we only power on (no shutdown in same loop).
	// When HIGH, we do nothing.
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
		managedNode("n3", true),
	)

	cfg := &config.Config{
		DryRun:          false, // real power-on path
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

	t.Run("allowed_when_low", func(t *testing.T) {
		node, cluster := 0.2, 0.3
		sh := &shutdownRecorder{}
		power := &mockPowerOnController{}
		r := &controller.Reconciler{
			Cfg:                   cfg,
			Client:                client,
			State:                 nodeops.NewNodeStateTracker(),
			Shutdowner:            sh,
			PowerOner:             power,
			DryRunNodeLoad:        &node,
			DryRunClusterLoadDown: &cluster,
		}

		r.MaybeRotate(context.Background())

		if len(sh.calls) != 0 {
			t.Fatalf("no shutdown should occur in same loop, got %v", sh.calls)
		}
		if got := power.PoweredOn; len(got) != 1 || got[0] != "off-old" {
			t.Fatalf("expected power-on of off-old, got %v", got)
		}
	})

	t.Run("blocked_when_high", func(t *testing.T) {
		node, cluster := 0.9, 0.9
		sh := &shutdownRecorder{}
		power := &mockPowerOnController{}
		r := &controller.Reconciler{
			Cfg:                   cfg,
			Client:                client,
			State:                 nodeops.NewNodeStateTracker(),
			Shutdowner:            sh,
			PowerOner:             power,
			DryRunNodeLoad:        &node,
			DryRunClusterLoadDown: &cluster,
		}

		r.MaybeRotate(context.Background())

		if len(sh.calls) != 0 {
			t.Fatalf("expected no shutdown, got %v", sh.calls)
		}
		if len(power.PoweredOn) != 0 {
			t.Fatalf("expected no power-on when loads are high, got %v", power.PoweredOn)
		}
	})
}

func TestMaybeRotate_RealRun_AppliesAnnotationAndCordon(t *testing.T) {
	// Now: first loop powers on overdue node and clears annotation; no shutdown yet.
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
		managedNode("n3", true),
	)

	cfg := &config.Config{
		DryRun:              false, // real power-on path
		MinNodes:            0,
		NodeLabels:          config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: "cba.dev/disabled"},
		NodeAnnotations:     config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:            config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{Enabled: false},
	}

	sh := &shutdownRecorder{}
	power := &mockPowerOnController{}
	r := &controller.Reconciler{
		Cfg:        cfg,
		Client:     client,
		State:      nodeops.NewNodeStateTracker(),
		Shutdowner: sh,
		PowerOner:  power,
	}

	r.MaybeRotate(context.Background())

	if len(sh.calls) != 0 {
		t.Fatalf("no shutdown should occur in same loop, got %v", sh.calls)
	}
	if got := power.PoweredOn; len(got) != 1 || got[0] != "off-old" {
		t.Fatalf("expected power-on of off-old, got %v", got)
	}

	// Check: annotation cleared & node uncordoned by power-on path.
	nodes, err := client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	var offOld *v1.Node
	for i := range nodes.Items {
		if nodes.Items[i].Name == "off-old" {
			offOld = &nodes.Items[i]
			break
		}
	}
	if offOld == nil {
		t.Fatal("off-old not found")
	}
	if offOld.Spec.Unschedulable {
		t.Fatalf("off-old should be uncordoned after power-on")
	}
	if val := offOld.Annotations[nodeops.AnnotationPoweredOff]; val != "" {
		t.Fatalf("powered-off annotation should be cleared, still: %q", val)
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
		DryRun:              false,
		MinNodes:            1, // enough capacity to rotate
		NodeLabels:          config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: disabledKey},
		NodeAnnotations:     config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:            config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute},
		IgnoreLabels:        map[string]string{ignoreKey: "true"},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{Enabled: false},
	}

	sh := &shutdownRecorder{}
	power := &mockPowerOnController{}
	r := &controller.Reconciler{
		Cfg:        cfg,
		Client:     client,
		State:      nodeops.NewNodeStateTracker(),
		Shutdowner: sh,
		PowerOner:  power,
	}

	r.MaybeRotate(context.Background())

	if len(sh.calls) != 0 {
		t.Fatalf("no shutdown should occur in same loop, got %v", sh.calls)
	}
	if got := power.PoweredOn; len(got) != 1 || got[0] != "off-old" {
		t.Fatalf("expected power-on of off-old, got %v", got)
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

func TestMaybeRotate_PowersOnOnly_FirstLoop(t *testing.T) {
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
	)

	cfg := &config.Config{
		DryRun:              false, // real power-on call
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

	require.Empty(t, rec.calls, "no shutdown should occur in the same loop as power-on")
	require.ElementsMatch(t, []string{"off-old"}, mockPower.PoweredOn, "overdue node should be powered on")
}

func TestMaybeRotate_LoadAvg_GatesPowerOn(t *testing.T) {
	client := corefake.NewSimpleClientset(
		poweredOffSince(managedNode("off-old", false), time.Now().Add(-2*time.Hour)),
		managedNode("n1", true),
		managedNode("n2", true),
		managedNode("n3", true),
	)

	baseCfg := &config.Config{
		DryRun:          false, // real power-on call
		MinNodes:        0,
		NodeLabels:      config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: "cba.dev/disabled"},
		NodeAnnotations: config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:        config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute},
		LoadAverageStrategy: config.LoadAverageStrategyConfig{
			Enabled:            true,
			NodeThreshold:      0.5,
			ScaleDownThreshold: 0.6,
			// ClusterEval not critical here; default "average" is fine
		},
	}

	t.Run("allowed when low", func(t *testing.T) {
		node, cluster := 0.2, 0.3
		rec := &shutdownRecorder{}
		mockPower := &mockPowerOnController{}
		r := &controller.Reconciler{
			Cfg:                   baseCfg,
			Client:                client,
			State:                 nodeops.NewNodeStateTracker(),
			Shutdowner:            rec,
			PowerOner:             mockPower,
			DryRunNodeLoad:        &node,
			DryRunClusterLoadDown: &cluster,
		}
		r.MaybeRotate(context.Background())
		require.Empty(t, rec.calls, "no shutdown in same loop")
		require.ElementsMatch(t, []string{"off-old"}, mockPower.PoweredOn, "power-on should happen when loads are below thresholds")
	})

	t.Run("blocked when high", func(t *testing.T) {
		node, cluster := 0.9, 0.9
		rec := &shutdownRecorder{}
		mockPower := &mockPowerOnController{}
		r := &controller.Reconciler{
			Cfg:                   baseCfg,
			Client:                client,
			State:                 nodeops.NewNodeStateTracker(),
			Shutdowner:            rec,
			PowerOner:             mockPower,
			DryRunNodeLoad:        &node,
			DryRunClusterLoadDown: &cluster,
		}
		r.MaybeRotate(context.Background())
		require.Empty(t, rec.calls, "no shutdown")
		require.Empty(t, mockPower.PoweredOn, "no power-on when loads are above thresholds")
	})
}

func TestMaybeRotate_RealRun_PowersOnAndClearsAnnotation(t *testing.T) {
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

	require.Empty(t, rec.calls, "no shutdown in the same loop")
	require.ElementsMatch(t, []string{"off-old"}, mockPower.PoweredOn)

	// Verify the powered-on node is uncordoned and its powered-off annotation cleared.
	nodes, err := client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	var offOld *v1.Node
	for i := range nodes.Items {
		if nodes.Items[i].Name == "off-old" {
			offOld = &nodes.Items[i]
			break
		}
	}
	require.NotNil(t, offOld, "off-old must exist")
	require.False(t, offOld.Spec.Unschedulable, "off-old should be uncordoned after power-on")
	require.Empty(t, offOld.Annotations[nodeops.AnnotationPoweredOff], "powered-off annotation should be cleared after power-on")
}

func TestMaybeRotate_SkipsIgnoredAndDisabled_PowerOnOnly(t *testing.T) {
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
		DryRun:              false,
		MinNodes:            1, // two eligibles (n1,n2) -> rotation allowed
		NodeLabels:          config.NodeLabelConfig{Managed: "cba.dev/is-managed", Disabled: disabledKey},
		NodeAnnotations:     config.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto},
		Rotation:            config.RotationConfig{Enabled: true, MaxPoweredOffDuration: 30 * time.Minute},
		IgnoreLabels:        map[string]string{ignoreKey: "true"},
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

	require.Empty(t, rec.calls, "no shutdown in same loop")
	require.ElementsMatch(t, []string{"off-old"}, mockPower.PoweredOn, "only the overdue node should be powered on")
}
