//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/controller"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	"github.com/docent-net/cluster-bare-autoscaler/test/integration/scenario"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

// Two-phase rotation across two loops:
// loop#1: no scale-down happens; rotation powers ON the oldest overdue node
// loop#2: after the node is Ready, one other node is shut down (not the fresh one)
func TestIntegration_TwoPhaseRotation_AcrossTwoLoops(t *testing.T) {
	ctx := context.Background()

	now := time.Now()
	off := scenario.PoweredOffSince(scenario.ManagedNode("off-old", false), now.Add(-8*time.Hour))
	// Ready; initial eligible will be 3:
	n1 := scenario.ManagedNode("n1", true)
	n2 := scenario.ManagedNode("n2", true)
	n3 := scenario.ManagedNode("n3", true)

	client := scenario.NewFakeClient(off, n1, n2, n3)

	cfg := scenario.MinimalConfig()
	cfg.MinNodes = 3 // eligible initially = 3 (n1,n2,n3) -> scale-down blocked in loop#1
	cfg.BootCooldown = 10 * time.Minute
	cfg.Rotation.Enabled = true
	cfg.Rotation.MaxPoweredOffDuration = 1 * time.Hour
	cfg.LoadAverageStrategy.Enabled = false
	sh := &scenario.ShutdownRecorder{}
	pwr := &scenario.PowerOnRecorder{}

	r := scenario.NewReconciler(cfg, client, sh, pwr)

	// ----- loop#1: run full reconcile (black-box) so order matches production.
	// Expect: no scale-down; rotation powers on "off-old"; no shutdown yet.
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("loop#1: reconcile error: %v", err)
	}

	if len(pwr.PoweredOn) != 1 || pwr.PoweredOn[0] != "off-old" {
		t.Fatalf("loop#1: expected power-on of off-old, got %v", pwr.PoweredOn)
	}
	if len(sh.Calls) != 0 {
		t.Fatalf("loop#1: expected no shutdown, got %v", sh.Calls)
	}

	// Mark "off-old" Ready (simulating it booted successfully).
	n, err := client.CoreV1().Nodes().Get(ctx, "off-old", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get off-old: %v", err)
	}
	n = scenario.MarkReady(n)
	if _, err := client.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update off-old ready: %v", err)
	}

	// Allow a retirement in loop #2 even while the fresh node is still under bootCooldown.
	// eligible (n1,n2,n3) == 3, so make minNodes lower than that.
	cfg.MinNodes = 2

	// Clear recorder slices to focus on loop#2 effects.
	pwr.PoweredOn = nil
	sh.Calls = nil

	// ----- loop#2: with off-old Ready and loads low, scale-down should retire exactly one node.
	// (Freshly booted node is protected by bootCooldown and won't be selected.)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("loop#2: reconcile error: %v", err)
	}

	if len(sh.Calls) != 1 {
		t.Fatalf("loop#2: expected exactly one shutdown, got %v", sh.Calls)
	}
	if sh.Calls[0] == "off-old" {
		t.Fatalf("loop#2: must not shut down the freshly booted node (got %q)", sh.Calls[0])
	}
}

// LoadAvg gates rotation decisions (low → allowed, high → blocked).
func TestIntegration_LoadAvg_GatesRotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	newEnv := func() (*scenario.PowerOnRecorder, *scenario.ShutdownRecorder, *controller.Reconciler) {
		now := time.Now()
		off := scenario.PoweredOffSince(scenario.ManagedNode("off-old", false), now.Add(-8*time.Hour))
		n1 := scenario.ManagedNode("n1", true)
		n2 := scenario.ManagedNode("n2", true)
		n3 := scenario.ManagedNode("n3", true)

		client := scenario.NewFakeClient(off, n1, n2, n3)

		cfg := scenario.MinimalConfig()
		cfg.MinNodes = 3 // block scale-down in loop#1 so rotation decides
		cfg.Rotation.Enabled = true
		cfg.Rotation.MaxPoweredOffDuration = 1 * time.Hour

		// Enable LoadAverage gating with clear thresholds
		cfg.LoadAverageStrategy.Enabled = true
		cfg.LoadAverageStrategy.NodeThreshold = 0.5
		cfg.LoadAverageStrategy.ScaleDownThreshold = 0.6
		cfg.LoadAverageStrategy.ClusterEval = "average"

		sh := &scenario.ShutdownRecorder{}
		pwr := &scenario.PowerOnRecorder{}
		r := scenario.NewReconciler(cfg, client, sh, pwr)
		return pwr, sh, r
	}

	t.Run("allowed when loads are low", func(t *testing.T) {
		pwr, sh, r := newEnv()
		low := 0.2
		r.DryRunNodeLoad = &low
		r.DryRunClusterLoadDown = &low

		if err := r.Reconcile(ctx); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if len(pwr.PoweredOn) != 1 || pwr.PoweredOn[0] != "off-old" {
			t.Fatalf("expected rotation to power on off-old, got %v", pwr.PoweredOn)
		}
		if len(sh.Calls) != 0 {
			t.Fatalf("expected no shutdown in same loop, got %v", sh.Calls)
		}
	})

	t.Run("blocked when cluster load is high", func(t *testing.T) {
		pwr, sh, r := newEnv()
		nodeLow := 0.2
		clusterHigh := 0.9
		r.DryRunNodeLoad = &nodeLow
		r.DryRunClusterLoadDown = &clusterHigh

		if err := r.Reconcile(ctx); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if len(pwr.PoweredOn) != 0 {
			t.Fatalf("expected rotation to be blocked by high cluster load, got power-ons: %v", pwr.PoweredOn)
		}
		if len(sh.Calls) != 0 {
			t.Fatalf("expected no shutdown either, got %v", sh.Calls)
		}
	})

	t.Run("blocked when candidate node load is high", func(t *testing.T) {
		pwr, sh, r := newEnv()
		nodeHigh := 0.9
		clusterLow := 0.2
		r.DryRunNodeLoad = &nodeHigh
		r.DryRunClusterLoadDown = &clusterLow

		if err := r.Reconcile(ctx); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if len(pwr.PoweredOn) != 0 {
			t.Fatalf("expected rotation to be blocked by high node load, got power-ons: %v", pwr.PoweredOn)
		}
		if len(sh.Calls) != 0 {
			t.Fatalf("expected no shutdown either, got %v", sh.Calls)
		}
	})
}

// 3) Aggregate exclusions affect decision (exclude CP from math).
func TestIntegration_AggregateExclusionsAffectDecision(t *testing.T) {
	t.Skip("TODO: set excludeFromAggregateLabels for control-plane; assert decision flips under high CP load")
}

// failing power-on stub used by TestIntegration_PowerOnFailure_NoShutdown
type errPowerOn struct{}

func (errPowerOn) PowerOn(ctx context.Context, nodeName, mac string) error {
	return fmt.Errorf("simulated power-on failure")
}

// MinNodes pre-boot guard blocks power-on when eligible+1 <= minNodes.
func TestIntegration_MinNodesPreBootGuardBlocksPowerOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Now()
	off := scenario.PoweredOffSince(scenario.ManagedNode("off-old", false), now.Add(-8*time.Hour))
	n1 := scenario.ManagedNode("n1", true)
	n2 := scenario.ManagedNode("n2", true)
	n3 := scenario.ManagedNode("n3", true)

	client := scenario.NewFakeClient(off, n1, n2, n3)

	cfg := scenario.MinimalConfig()
	// eligible = 3 (n1,n2,n3). With minNodes = 4, eligible+1 == 4 → guard should block rotation.
	cfg.MinNodes = 4
	cfg.Rotation.Enabled = true
	cfg.Rotation.MaxPoweredOffDuration = 1 * time.Hour
	cfg.LoadAverageStrategy.Enabled = false // avoid metrics dependency

	sh := &scenario.ShutdownRecorder{}
	pwr := &scenario.PowerOnRecorder{}
	r := scenario.NewReconciler(cfg, client, sh, pwr)

	// Call MaybeRotate directly to test the pre-boot capacity guard (avoid MinNodeCount scale-up).
	r.MaybeRotate(ctx)

	if len(pwr.PoweredOn) != 0 {
		t.Fatalf("expected NO power-on due to pre-boot minNodes guard, got %v", pwr.PoweredOn)
	}
	if len(sh.Calls) != 0 {
		t.Fatalf("expected no shutdown either, got %v", sh.Calls)
	}
}

// Overdue node with rotation.exemptLabel is skipped.
func TestIntegration_RotationExemptSkipsOverdue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Now()
	overdue := scenario.PoweredOffSince(scenario.ManagedNode("off-old", false), now.Add(-8*time.Hour))
	if overdue.Labels == nil {
		overdue.Labels = map[string]string{}
	}
	overdue.Labels["cba.dev/rotation-exempt"] = "true"

	n1 := scenario.ManagedNode("n1", true)
	n2 := scenario.ManagedNode("n2", true)
	n3 := scenario.ManagedNode("n3", true)

	client := scenario.NewFakeClient(overdue, n1, n2, n3)

	cfg := scenario.MinimalConfig()
	cfg.MinNodes = 3 // block scale-down so rotation decides
	cfg.Rotation.Enabled = true
	cfg.Rotation.MaxPoweredOffDuration = 1 * time.Hour
	cfg.Rotation.ExemptLabel = "cba.dev/rotation-exempt"
	cfg.LoadAverageStrategy.Enabled = false

	sh := &scenario.ShutdownRecorder{}
	pwr := &scenario.PowerOnRecorder{}
	r := scenario.NewReconciler(cfg, client, sh, pwr)

	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(pwr.PoweredOn) != 0 {
		t.Fatalf("expected NO power-on because overdue node is exempt, got %v", pwr.PoweredOn)
	}
	if len(sh.Calls) != 0 {
		t.Fatalf("expected no shutdown either, got %v", sh.Calls)
	}
}

// Power-on failure aborts rotation (no shutdown, state unchanged).
func TestIntegration_PowerOnFailure_NoShutdown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Now()
	off := scenario.PoweredOffSince(scenario.ManagedNode("off-old", false), now.Add(-8*time.Hour))
	n1 := scenario.ManagedNode("n1", true)
	n2 := scenario.ManagedNode("n2", true)
	n3 := scenario.ManagedNode("n3", true)

	client := scenario.NewFakeClient(off, n1, n2, n3)

	cfg := scenario.MinimalConfig()
	cfg.MinNodes = 3
	cfg.Rotation.Enabled = true
	cfg.Rotation.MaxPoweredOffDuration = 1 * time.Hour
	cfg.LoadAverageStrategy.Enabled = false

	sh := &scenario.ShutdownRecorder{}

	r := controller.NewReconciler(
		cfg,
		client,
		metricsfake.NewSimpleClientset(),
		func(r *controller.Reconciler) { r.Shutdowner = sh },
		func(r *controller.Reconciler) { r.PowerOner = errPowerOn{} }, // inject failing power-on
	)

	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(sh.Calls) != 0 {
		t.Fatalf("expected no shutdown when power-on fails, got %v", sh.Calls)
	}

	n, err := client.CoreV1().Nodes().Get(ctx, "off-old", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if _, ok := n.Annotations[nodeops.AnnotationPoweredOff]; !ok {
		t.Fatalf("expected powered-off annotation to remain after power-on failure")
	}
}

func TestIntegration_GlobalCooldown_SkipsWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	n1 := scenario.ManagedNode("n1", true)
	n2 := scenario.ManagedNode("n2", true)
	client := scenario.NewFakeClient(n1, n2)

	cfg := scenario.MinimalConfig()
	cfg.Cooldown = 10 * time.Minute
	cfg.LoadAverageStrategy.Enabled = false
	cfg.Rotation.Enabled = true

	sh := &scenario.ShutdownRecorder{}
	pwr := &scenario.PowerOnRecorder{}
	r := scenario.NewReconciler(cfg, client, sh, pwr)

	// Simulate a very recent shutdown so cooldown is active.
	r.State.MarkGlobalShutdown()

	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(pwr.PoweredOn) != 0 || len(sh.Calls) != 0 {
		t.Fatalf("expected cooldown to short-circuit; powerOns=%v shutdowns=%v", pwr.PoweredOn, sh.Calls)
	}
}

func requireNoErr(t *testing.T, err error) {
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func contains(ss []string, needle string) bool {
	for _, s := range ss {
		if s == needle {
			return true
		}
	}
	return false
}
func TestIntegration_LoadAvgScaleDown_DeniesOnHighAggregate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 3 ready + 1 off, minNodes=2 → scale-down is normally allowed.
	now := time.Now()
	off := scenario.PoweredOffSince(scenario.ManagedNode("off-old", false), now.Add(-8*time.Hour))
	n1 := scenario.ManagedNode("n1", true)
	n2 := scenario.ManagedNode("n2", true)
	n3 := scenario.ManagedNode("n3", true)
	client := scenario.NewFakeClient(off, n1, n2, n3)

	cfg := scenario.MinimalConfig()
	cfg.MinNodes = 2
	cfg.Rotation.Enabled = false

	cfg.LoadAverageStrategy.Enabled = true
	cfg.LoadAverageStrategy.NodeThreshold = 1.0      // node load never blocks
	cfg.LoadAverageStrategy.ScaleDownThreshold = 0.8 // aggregate gate
	cfg.LoadAverageStrategy.ClusterEval = "average"

	sh := &scenario.ShutdownRecorder{}
	pwr := &scenario.PowerOnRecorder{}
	r := scenario.NewReconciler(cfg, client, sh, pwr)

	// Drive aggregate high via dry-run override; no shutdown should occur.
	hi := 0.95
	r.DryRunClusterLoadDown = &hi

	requireNoErr(t, r.Reconcile(ctx))
	if len(sh.Calls) != 0 {
		t.Fatalf("expected no shutdown due to high aggregate, got %v", sh.Calls)
	}
}
func TestIntegration_BootCooldown_ProtectsFreshNodeFromShutdown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Now()
	off := scenario.PoweredOffSince(scenario.ManagedNode("off-old", false), now.Add(-8*time.Hour))
	n1 := scenario.ManagedNode("n1", true)
	n2 := scenario.ManagedNode("n2", true)
	n3 := scenario.ManagedNode("n3", true)
	client := scenario.NewFakeClient(off, n1, n2, n3)

	cfg := scenario.MinimalConfig()
	cfg.MinNodes = 3
	cfg.Rotation.Enabled = true
	cfg.Rotation.MaxPoweredOffDuration = 1 * time.Hour
	cfg.BootCooldown = 30 * time.Minute
	cfg.LoadAverageStrategy.Enabled = false

	sh := &scenario.ShutdownRecorder{}
	pwr := &scenario.PowerOnRecorder{}
	r := scenario.NewReconciler(cfg, client, sh, pwr)

	// loop #1: rotation powers on off-old
	requireNoErr(t, r.Reconcile(ctx))
	if !contains(pwr.PoweredOn, "off-old") {
		t.Fatalf("expected power-on of off-old, got %v", pwr.PoweredOn)
	}

	// Make it Ready, but bootCooldown still in effect.
	n, _ := client.CoreV1().Nodes().Get(ctx, "off-old", metav1.GetOptions{})
	n = scenario.MarkReady(n)
	_, _ = client.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{})

	// Allow one retirement in loop #2.
	cfg.MinNodes = 2
	pwr.PoweredOn = nil
	sh.Calls = nil

	requireNoErr(t, r.Reconcile(ctx))

	if len(sh.Calls) != 1 {
		t.Fatalf("expected exactly one shutdown, got %v", sh.Calls)
	}
	if sh.Calls[0] == "off-old" {
		t.Fatalf("boot-cooled fresh node should not be retired, got %v", sh.Calls)
	}
}

// ForcePowerOnAllNodes powers all managed nodes (TODO)
func TestIntegration_ForcePowerOnAllNodes_PowersAllManaged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Managed nodes: two Ready, one NotReady (treated as powered-off)
	n1 := scenario.ManagedNode("n1", true)
	n2 := scenario.ManagedNode("n2", true)
	off := scenario.ManagedNode("off-old", false)

	client := scenario.NewFakeClient(n1, n2, off)

	cfg := scenario.MinimalConfig()
	cfg.ForcePowerOnAllNodes = true
	cfg.Rotation.Enabled = false
	cfg.LoadAverageStrategy.Enabled = false

	sh := &scenario.ShutdownRecorder{}
	pwr := &scenario.PowerOnRecorder{}
	r := scenario.NewReconciler(cfg, client, sh, pwr)

	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Expect power-on only for managed NotReady nodes (Ready nodes are skipped as no-op).
	want := map[string]struct{}{"off-old": {}}
	got := map[string]struct{}{}
	for _, name := range pwr.PoweredOn {
		got[name] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("expected power-on only for NotReady managed nodes; got=%v", pwr.PoweredOn)
	}
	if _, ok := got["off-old"]; !ok {
		t.Fatalf("missing power-on for node %q; calls=%v", "off-old", pwr.PoweredOn)
	}
	if contains(pwr.PoweredOn, "n1") || contains(pwr.PoweredOn, "n2") {
		t.Fatalf("Ready nodes should not be force-powered on; got=%v", pwr.PoweredOn)
	}

	// No shutdowns in this path.
	if len(sh.Calls) != 0 {
		t.Fatalf("expected no shutdowns; got=%v", sh.Calls)
	}
}

// LoadAverage scale-up gating
func TestIntegration_LoadAvgScaleUp_GatesByClusterLoad(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Two Ready nodes and one explicitly powered-off candidate.
	n1 := scenario.ManagedNode("n1", true)
	n2 := scenario.ManagedNode("n2", true)
	now := time.Now()
	off := scenario.PoweredOffSince(scenario.ManagedNode("off-old", false), now.Add(-1*time.Hour))

	client := scenario.NewFakeClient(n1, n2, off)

	// Base config
	base := scenario.MinimalConfig()
	base.MinNodes = 2             // MinNodeCount alone should NOT trigger scale-up
	base.Rotation.Enabled = false // focus purely on scale-up logic
	base.LoadAverageStrategy.Enabled = true
	base.LoadAverageStrategy.ScaleUpThreshold = 0.7
	base.LoadAverageStrategy.ClusterEval = "average"

	t.Run("low cluster load denies scale-up", func(t *testing.T) {
		sh := &scenario.ShutdownRecorder{}
		pwr := &scenario.PowerOnRecorder{}

		// Build reconciler with dry-run override applied BEFORE strategies are built.
		rLow := controller.NewReconciler(
			base,
			client,
			metricsfake.NewSimpleClientset(),
			func(r *controller.Reconciler) { r.Shutdowner = sh },
			func(r *controller.Reconciler) { r.PowerOner = pwr },
			controller.WithDryRunClusterLoadUp(0.3),
		)

		if err := rLow.Reconcile(ctx); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if len(pwr.PoweredOn) != 0 {
			t.Fatalf("expected NO power-on under low cluster load, got %v", pwr.PoweredOn)
		}
	})

	t.Run("high cluster load triggers scale-up", func(t *testing.T) {
		sh := &scenario.ShutdownRecorder{}
		pwr := &scenario.PowerOnRecorder{}

		// Fresh reconciler with high override.
		rHigh := controller.NewReconciler(
			base,
			client,
			metricsfake.NewSimpleClientset(),
			func(r *controller.Reconciler) { r.Shutdowner = sh },
			func(r *controller.Reconciler) { r.PowerOner = pwr },
			controller.WithDryRunClusterLoadUp(0.9),
		)

		if err := rHigh.Reconcile(ctx); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if !contains(pwr.PoweredOn, "off-old") {
			t.Fatalf("expected power-on of off-old under high cluster load, got %v", pwr.PoweredOn)
		}
	})
}
