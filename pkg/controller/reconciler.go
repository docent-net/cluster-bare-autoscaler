package controller

import (
	"context"
	"errors"
	"fmt"
	"github.com/docent-net/cluster-bare-autoscaler/internal/bootstrap/metrics"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	"k8s.io/client-go/util/retry"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	policyv1 "k8s.io/api/policy/v1"
	"log/slog"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/power"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/strategy"
)

type Reconciler struct {
	Cfg                   *config.Config
	Client                kubernetes.Interface
	Shutdowner            power.ShutdownController
	PowerOner             power.PowerOnController
	State                 *nodeops.NodeStateTracker
	Metrics               metrics.Interface
	ScaleDownStrategy     strategy.ScaleDownStrategy
	ScaleUpStrategy       strategy.ScaleUpStrategy
	DryRunNodeLoad        *float64 // optional CLI override
	DryRunClusterLoadDown *float64 // CLI override for scale-down
	DryRunClusterLoadUp   *float64 // CLI override for scale-up
}

type ReconcilerOption func(r *Reconciler)

func NewReconciler(cfg *config.Config, client kubernetes.Interface, metricsClient metricsclient.Interface, opts ...ReconcilerOption) *Reconciler {
	shutdowner, powerOner := power.NewControllersFromConfig(cfg, client)
	r := &Reconciler{
		Cfg:        cfg,
		Client:     client,
		State:      nodeops.NewNodeStateTracker(),
		Shutdowner: shutdowner,
		PowerOner:  powerOner,
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	r.ScaleDownStrategy = buildScaleDownStrategy(cfg, client, metricsClient, r)
	r.ScaleUpStrategy = buildScaleUpStrategy(cfg, r)

	r.RestorePoweredOffState(context.Background())
	return r
}

// buildScaleDownStrategy constructs a composite scale-down strategy based on the current config.
// It includes the ResourceAwareScaleDown strategy by default to ensure pods can fit elsewhere.
// If LoadAverageStrategy is enabled, it adds LoadAverageScaleDown, which shuts down nodes
// based on normalized per-node and cluster-wide load averages.
// Supports dry-run overrides and evaluates multiple strategies using a MultiStrategy chain.
func buildScaleDownStrategy(cfg *config.Config, client kubernetes.Interface, metricsClient metricsclient.Interface, r *Reconciler) strategy.ScaleDownStrategy {
	var strategies []strategy.ScaleDownStrategy

	strategies = append(strategies, &strategy.ResourceAwareScaleDown{
		Client:        client,
		MetricsClient: metricsClient,
		Cfg:           cfg,
		NodeLister: func(ctx context.Context) ([]v1.Node, error) {
			list, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				return nil, err
			}
			return list.Items, nil
		},
		PodLister: func(ctx context.Context) ([]v1.Pod, error) {
			list, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
			if err != nil {
				return nil, err
			}
			return list.Items, nil
		},
	})

	if cfg.LoadAverageStrategy.Enabled {
		strategies = append(strategies, &strategy.LoadAverageScaleDown{
			Client:                    client,
			Cfg:                       cfg,
			PodLabel:                  cfg.LoadAverageStrategy.PodLabel,
			Namespace:                 cfg.LoadAverageStrategy.Namespace,
			HTTPPort:                  cfg.LoadAverageStrategy.Port,
			HTTPTimeout:               time.Duration(cfg.LoadAverageStrategy.TimeoutSeconds) * time.Second,
			NodeThreshold:             cfg.LoadAverageStrategy.NodeThreshold,
			ClusterWideThreshold:      cfg.LoadAverageStrategy.ScaleDownThreshold,
			DryRunNodeLoadOverride:    r.DryRunNodeLoad,
			DryRunClusterLoadOverride: r.DryRunClusterLoadDown,
			IgnoreLabels:              BuildAggregateExclusions(cfg),
			ClusterEvalMode:           strategy.ParseClusterEvalMode(cfg.LoadAverageStrategy.ClusterEval),
		})
	}

	names := []string{}
	for _, s := range strategies {
		names = append(names, s.Name())
	}
	slog.Info("Configured scale-down strategy chain", "strategies", names)

	return &strategy.MultiStrategy{Strategies: strategies}
}

// buildScaleUpStrategy constructs a composite scale-up strategy based on the current config.
// It always includes MinNodeCountScaleUp to maintain the minimum required nodes,
// and optionally includes LoadAverageScaleUp if enabled, which powers on nodes based on
// cluster-wide load average. Dry-run overrides for cluster-wide load are respected.
// The resulting strategy is a MultiUpStrategy that evaluates all sub-strategies in order.
func buildScaleUpStrategy(cfg *config.Config, r *Reconciler) strategy.ScaleUpStrategy {
	upStrategies := []strategy.ScaleUpStrategy{
		&strategy.MinNodeCountScaleUp{
			Cfg:          r.Cfg,
			ActiveNodes:  r.listActiveNodes,
			ShutdownList: r.shutdownNodeNames,
		},
	}

	if cfg.LoadAverageStrategy.Enabled {
		upStrategies = append(upStrategies, &strategy.LoadAverageScaleUp{
			Client:               r.Client,
			Namespace:            cfg.LoadAverageStrategy.Namespace,
			PodLabel:             cfg.LoadAverageStrategy.PodLabel,
			HTTPPort:             cfg.LoadAverageStrategy.Port,
			HTTPTimeout:          time.Duration(cfg.LoadAverageStrategy.TimeoutSeconds) * time.Second,
			ClusterEvalMode:      strategy.ParseClusterEvalMode(cfg.LoadAverageStrategy.ClusterEval),
			ClusterWideThreshold: cfg.LoadAverageStrategy.ScaleUpThreshold,
			DryRunOverride:       r.DryRunClusterLoadUp,
			IgnoreLabels:         BuildAggregateExclusions(cfg),
			ShutdownCandidates:   r.shutdownNodeNames,
		})
	}

	names := []string{}
	for _, s := range upStrategies {
		names = append(names, s.Name())
	}
	slog.Info("Configured scale-up strategy chain", "strategies", names)

	return &strategy.MultiUpStrategy{Strategies: upStrategies}
}

func (r *Reconciler) Reconcile(ctx context.Context) error {
	now := time.Now()

	if err := nodeops.RecoverUnexpectedlyBootedNodes(ctx, r.Client, r.Cfg, r.Cfg.DryRun); err != nil {
		slog.Warn("Failed to recover unexpectedly booted nodes", "err", err)
		return nil
	}

	if r.Cfg.ForcePowerOnAllNodes {
		slog.Info("Force power-on of all managed nodes enabled")
		err := nodeops.ForcePowerOnAllNodes(ctx, r.Client, r.Cfg, r.State, r.PowerOner, r.Cfg.DryRun)
		if err != nil {
			slog.Warn("Failed to force power on all nodes", "err", err)
		}

		return nil
	}

	if r.State.IsGlobalCooldownActive(now, r.Cfg.Cooldown) {
		remaining := r.Cfg.Cooldown - now.Sub(r.State.LastShutdownTime)
		slog.Info("Global cooldown active — skipping reconcile loop", "remaining", remaining.Round(time.Second).String())
		return nil
	}

	slog.Info("Running reconcile loop")
	metrics.Evaluations.Inc()

	if r.MaybeScaleUp(ctx) {
		return nil // stop here to avoid scaling up in the same loop
	}

	allNodes, err := r.listAllNodes(ctx)
	if err != nil {
		return err
	}

	eligible := r.filterEligibleNodes(allNodes.Items)
	if r.MaybeScaleDown(ctx, eligible) {
		return nil
	}

	// maintenance: rotate overdue powered-off nodes
	r.MaybeRotate(ctx)

	return nil
}

func (r *Reconciler) RestorePoweredOffState(ctx context.Context) {
	nodeList, err := r.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Warn("Failed to list nodes while restoring powered-off State", "err", err)
		return
	}

	// Build a map of currently running nodes
	active := make(map[string]struct{})
	for _, node := range nodeList.Items {
		active[node.Name] = struct{}{}
	}

	managed, err := nodeops.ListManagedNodes(ctx, r.Client, nodeops.ManagedNodeFilter{
		ManagedLabel:  r.Cfg.NodeLabels.Managed,
		DisabledLabel: r.Cfg.NodeLabels.Disabled,
		IgnoreLabels:  r.Cfg.IgnoreLabels,
	})
	if err != nil {
		slog.Warn("Failed to list managed nodes during restore", "err", err)
		return
	}
	for _, node := range managed {
		if _, found := active[node.Name]; !found {
			slog.Info("Managed node not found in active set — assuming powered off", "node", node.Name)
			r.State.MarkPoweredOff(node.Name)
		}
	}
}

func (r *Reconciler) filterEligibleNodes(nodes []v1.Node) []*nodeops.NodeWrapper {
	eligible := nodeops.FilterShutdownEligibleNodes(nodes, r.State, time.Now(), nodeops.EligibilityConfig{
		Cooldown:     r.Cfg.Cooldown,
		BootCooldown: r.Cfg.BootCooldown,
		IgnoreLabels: r.Cfg.IgnoreLabels,
	})
	slog.Info("Filtered nodes", "eligible", len(eligible), "total", len(nodes))
	return eligible
}

func (r *Reconciler) listAllNodes(ctx context.Context) (*v1.NodeList, error) {
	nodes, err := nodeops.ListManagedNodes(ctx, r.Client, nodeops.ManagedNodeFilter{
		ManagedLabel:  r.Cfg.NodeLabels.Managed,
		DisabledLabel: r.Cfg.NodeLabels.Disabled,
		IgnoreLabels:  r.Cfg.IgnoreLabels,
	})
	if err != nil {
		slog.Error("failed to list managed nodes", "err", err)
		return nil, err
	}
	return &v1.NodeList{Items: nodes}, nil
}

func (r *Reconciler) listActiveNodes(ctx context.Context) ([]v1.Node, error) {
	return nodeops.ListActiveNodes(ctx, r.Client, r.State, nodeops.ManagedNodeFilter{
		ManagedLabel:  r.Cfg.NodeLabels.Managed,
		DisabledLabel: r.Cfg.NodeLabels.Disabled,
		IgnoreLabels:  r.Cfg.IgnoreLabels,
	}, nodeops.ActiveNodeFilter{
		IgnoreLabels: r.Cfg.IgnoreLabels,
	})
}

func (r *Reconciler) shutdownNodeNames(ctx context.Context) []string {
	nodes, err := nodeops.ListShutdownNodeNames(ctx, r.Client, nodeops.ManagedNodeFilter{
		ManagedLabel:  r.Cfg.NodeLabels.Managed,
		DisabledLabel: r.Cfg.NodeLabels.Disabled,
		IgnoreLabels:  r.Cfg.IgnoreLabels,
	}, r.State)

	if err != nil {
		slog.Warn("Failed to list shutdown nodes", "err", err)
		return nil
	}
	return nodes
}

func (r *Reconciler) MaybeScaleUp(ctx context.Context) bool {
	nodeName, shouldScale, err := r.ScaleUpStrategy.ShouldScaleUp(ctx)
	if err != nil {
		slog.Error("Scale-up strategy error", "err", err)
		return false
	}
	if !shouldScale {
		slog.Info("No scale-up possible", "reason", "all strategies denied", "minNodes", r.Cfg.MinNodes)
		return false
	}

	slog.Info("Attempting scale-up", "node", nodeName)

	node, err := r.Client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		slog.Error("Failed to get node object for scale-up", "node", nodeName, "err", err)
		return false
	}

	wrapped := nodeops.NewNodeWrapper(node, r.State, time.Now(), nodeops.NodeAnnotationConfig{
		MAC: r.Cfg.NodeAnnotations.MAC,
	}, r.Cfg.IgnoreLabels)

	if err := nodeops.PowerOnAndMarkBooted(ctx, wrapped, r.Cfg, r.Client, r.PowerOner, r.State, r.Cfg.DryRun); err != nil {
		slog.Error("PowerOnAndMarkBooted failed", "node", nodeName, "err", err)
		return false
	}

	// Manual: Clear shutdown state and metrics here
	r.State.ClearPoweredOff(nodeName)
	metrics.PoweredOffNodes.WithLabelValues(nodeName).Set(0)

	slog.Info("Scale-up complete", "node", nodeName)
	return true
}

func (r *Reconciler) MaybeScaleDown(ctx context.Context, eligible []*nodeops.NodeWrapper) bool {
	candidate := r.PickScaleDownCandidate(eligible)
	if candidate == nil {
		slog.Info("No scale-down possible", "eligible", len(eligible), "minNodes", r.Cfg.MinNodes)
		return false
	}

	ok, err := r.ScaleDownStrategy.
		ShouldScaleDown(ctx, candidate.Name)
	if err != nil {
		slog.Error("Scale-down strategy failed", "err", err)
		return false
	}
	if !ok {
		slog.Info("Scale-down strategy: node not eligible", "node", candidate.Name)
		return false
	}

	slog.Info("Candidate for scale-down", "node", candidate.Name)
	metrics.ScaleDowns.Inc()

	if err := r.CordonAndDrain(ctx, candidate); err != nil {
		slog.Warn("CordonAndDrain failed", "node", candidate.Name, "err", err)
		if err := nodeops.ClearPoweredOffAnnotation(ctx, r.Client, candidate.Name); err != nil {
			slog.Warn("Failed to clear annotation from powered-off node", "node", candidate.Name, "err", err)
		}
		return false
	}

	if err := r.AnnotatePoweredOffNode(ctx, candidate); err != nil {
		slog.Warn("Failed to annotate powered-off node", "node", candidate.Name, "err", err)
	}

	metrics.ShutdownAttempts.Inc()
	if err := r.Shutdowner.Shutdown(ctx, candidate.Name); err != nil {
		slog.Error("Shutdown failed", "node", candidate.Name, "err", err)
		if err := nodeops.ClearPoweredOffAnnotation(ctx, r.Client, candidate.Name); err != nil {
			slog.Warn("Failed to clear annotation from powered-off node", "node", candidate.Name, "err", err)
		}
	} else {
		slog.Info("Shutdown initiated", "node", candidate.Name)
		metrics.ShutdownSuccesses.Inc()
		metrics.PoweredOffNodes.WithLabelValues(candidate.Name).Set(1)
		r.State.MarkGlobalShutdown()
	}

	if !r.Cfg.DryRun {
		r.State.MarkShutdown(candidate.Name)
		r.State.MarkPoweredOff(candidate.Name)
	}

	return true
}

func (r *Reconciler) AnnotatePoweredOffNode(ctx context.Context, node *nodeops.NodeWrapper) error {
	if r.Cfg.DryRun {
		slog.Debug("Dry-run: would annotate node as powered-off", "node", node.Name)
		return nil
	}
	slog.Debug("Annotating node as powered-off", "node", node.Name)
	timestamp := time.Now().UTC().Format(time.RFC3339)
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, nodeops.AnnotationPoweredOff, timestamp))
	_, err := r.Client.CoreV1().Nodes().Patch(ctx, node.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *Reconciler) PickScaleDownCandidate(eligible []*nodeops.NodeWrapper) *nodeops.NodeWrapper {
	if len(eligible) <= r.Cfg.MinNodes {
		return nil
	}
	return eligible[len(eligible)-1]
}

func (r *Reconciler) CordonAndDrain(ctx context.Context, node *nodeops.NodeWrapper) error {
	// Step 1: Cordon
	if r.Cfg.DryRun {
		slog.Info("Dry-run: would cordon node", "node", node.Name)
	} else {
		err := retry.OnError(retry.DefaultBackoff, apierrors.IsConflict, func() error {
			latest, err := r.Client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			latestCopy := latest.DeepCopy()
			latestCopy.Spec.Unschedulable = true
			_, err = r.Client.CoreV1().Nodes().Update(ctx, latestCopy, metav1.UpdateOptions{})
			return err
		})
		if err != nil {
			slog.Error("Failed to cordon node after retries", "node", node.Name, "err", err)
			return err
		}
		slog.Info("Node cordoned", "node", node.Name)
	}

	// Step 2: List pods on node
	pods, err := r.Client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + node.Name,
	})
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		// Skip mirror pods
		if _, ok := pod.Annotations["kubernetes.io/config.mirror"]; ok {
			slog.Info("Skipping mirror pod", "pod", pod.Name)
			continue
		}
		// Skip DaemonSet pods
		if ref := metav1.GetControllerOf(&pod); ref != nil && ref.Kind == "DaemonSet" {
			slog.Info("Skipping DaemonSet pod", "pod", pod.Name)
			continue
		}

		// Try eviction
		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
			DeleteOptions: &metav1.DeleteOptions{},
		}

		if r.Cfg.DryRun {
			slog.Info("Dry-run: would evict pod", "pod", pod.Name, "ns", pod.Namespace)
		} else {
			err := r.Client.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
			if err != nil {
				slog.Warn("Eviction failed", "pod", pod.Name, "err", err)
				return errors.New("aborting drain due to eviction failure")
			}
			slog.Info("Evicted pod", "pod", pod.Name, "ns", pod.Namespace)
		}
	}

	slog.Info("Node drained successfully", "node", node.Name)
	return nil
}

// MaybeRotate performs a maintenance rotation in two phases.
// Phase in this loop:
//   - Find an overdue powered-off node (age >= rotation.maxPoweredOffDuration), honoring exempt & ignore labels.
//   - Ensure capacity safety: (eligible + 1) > minNodes and, if LoadAverage is enabled, that at least one
//     tentative retire candidate passes the load gates.
//   - Power ON the overdue node first and RETURN immediately.
//
// A later reconcile loop (after readiness + cooldown) may retire one eligible active node via normal logic.
func (r *Reconciler) MaybeRotate(ctx context.Context) {
	if r.Cfg == nil || !r.Cfg.Rotation.Enabled || r.Cfg.Rotation.MaxPoweredOffDuration <= 0 {
		return
	}

	slog.Debug("MaybeRotate: start",
		"enabled", r.Cfg.Rotation.Enabled,
		"maxOffAge", r.Cfg.Rotation.MaxPoweredOffDuration.String(),
		"exemptLabel", r.Cfg.Rotation.ExemptLabel,
	)
	now := time.Now().UTC()

	// 1) Discover the oldest overdue powered-off node.
	managed, err := nodeops.ListManagedNodes(ctx, r.Client, nodeops.ManagedNodeFilter{
		ManagedLabel:  r.Cfg.NodeLabels.Managed,
		DisabledLabel: r.Cfg.NodeLabels.Disabled,
		IgnoreLabels:  r.Cfg.IgnoreLabels,
	})
	if err != nil || len(managed) == 0 {
		if err != nil {
			slog.Warn("MaybeRotate: listing managed nodes failed", "err", err)
		}
		return
	}
	slog.Debug("MaybeRotate: managed nodes fetched", "count", len(managed))

	var (
		overdue *v1.Node
		since   time.Time
	)
	poweredOffCount := 0
	overdueCount := 0

	for i := range managed {
		n := managed[i]

		// Per-node exemption.
		if key := r.Cfg.Rotation.ExemptLabel; key != "" {
			if val, ok := n.Labels[key]; ok && val != "" {
				slog.Debug("MaybeRotate: skip node due to exemptLabel", "node", n.Name, "label", key)
				continue
			}
		}
		// Honor global ignore labels.
		if nodeops.ShouldIgnoreNodeDueToLabels(n, r.Cfg.IgnoreLabels) {
			slog.Debug("MaybeRotate: skip node due to ignoreLabels", "node", n.Name)
			continue
		}

		if t, ok := nodeops.PoweredOffSince(n); ok {
			poweredOffCount++
			age := now.Sub(t)
			if age >= r.Cfg.Rotation.MaxPoweredOffDuration {
				overdueCount++
				if overdue == nil || t.Before(since) {
					overdue = &managed[i]
					since = t
				}
			}
		}
	}

	if overdue == nil {
		slog.Info("MaybeRotate: no overdue powered-off node found",
			"poweredOff", poweredOffCount,
			"overdue", overdueCount,
			"minOffAge", r.Cfg.Rotation.MaxPoweredOffDuration.String(),
		)
		return
	}

	// 2) Capacity safety before we consider booting another node.
	allNodes, err := r.listAllNodes(ctx)
	if err != nil {
		slog.Warn("MaybeRotate: list nodes failed", "err", err)
		return
	}
	eligible := r.filterEligibleNodes(allNodes.Items)
	slog.Debug("MaybeRotate: pre-power-on capacity check", "eligible", len(eligible), "minNodes", r.Cfg.MinNodes)

	// Allow rotation if adding one node would put us strictly above minNodes.
	if len(eligible)+1 <= r.Cfg.MinNodes {
		slog.Info("MaybeRotate: skip — eligible+1 at/below minNodes",
			"eligible", len(eligible), "minNodes", r.Cfg.MinNodes)
		return
	}

	// 3) If LoadAverage is enabled, require that at least one tentative retire candidate passes the gates.
	// (We don't retire now — this just ensures we *could* safely retire later.)
	slog.Debug("MaybeRotate: evaluating tentative retire candidates", "eligible", len(eligible))
	cand := r.PickRotationPoweroffCandidate(ctx, eligible)
	if cand == nil {
		slog.Info("MaybeRotate: skip — no suitable tentative retire candidate (gates/eligibility)")
		return
	}
	slog.Debug("MaybeRotate: tentative retire candidate selected", "node", cand.Name)

	// 4) Power ON the overdue node first, then RETURN (two-phase rotation).
	slog.Info("MaybeRotate: powering on overdue node",
		"node", overdue.Name, "poweredOffSince", since, "offAge", now.Sub(since).Round(time.Second).String())

	wrapped := nodeops.NewNodeWrapper(overdue, r.State, now, nodeops.NodeAnnotationConfig{
		MAC: r.Cfg.NodeAnnotations.MAC,
	}, r.Cfg.IgnoreLabels)

	if err := nodeops.PowerOnAndMarkBooted(ctx, wrapped, r.Cfg, r.Client, r.PowerOner, r.State, r.Cfg.DryRun); err != nil {
		slog.Warn("MaybeRotate: power-on failed; abort", "node", overdue.Name, "err", err)
		return
	}

	// Clear powered-off state/metric like in scale-up.
	r.State.ClearPoweredOff(overdue.Name)
	metrics.PoweredOffNodes.WithLabelValues(overdue.Name).Set(0)

	// Two-phase: do not retire in the same loop. Reconcile()'s global cooldown guard + per-node boot cooldown
	// ensure stabilization before any shutdown is considered later.
	slog.Info("MaybeRotate: powered on overdue node; will consider shutdown after readiness and cooldown")
	return
}

// PickRotationPoweroffCandidate applies optional LoadAverage checks to find a safe node to power off.
// If LoadAverage is disabled, it defers to the default scale-down candidate picker.
// When enabled, a candidate is accepted only if:
//   - candidate node normalized load < NodeThreshold, and
//   - cluster aggregate load (excluding the candidate and excluding disabled nodes) < ScaleDownThreshold.
//
// If aggregate is already too high with one candidate excluded, we abort early (return nil).
func (r *Reconciler) PickRotationPoweroffCandidate(ctx context.Context, eligible []*nodeops.NodeWrapper) *nodeops.NodeWrapper {
	// If LoadAverage strategy is disabled, keep existing selection behavior.
	if !r.Cfg.LoadAverageStrategy.Enabled {
		return r.PickScaleDownCandidate(eligible)
	}

	// Debug: show candidate set we will evaluate under load-average gates.
	if len(eligible) > 0 {
		names := make([]string, 0, len(eligible))
		for _, e := range eligible {
			names = append(names, e.Name)
		}
		slog.Debug("MaybeRotate: LoadAvg candidate set", "count", len(eligible), "names", names)
	}

	// Build helpers once.
	utils := strategy.NewClusterLoadUtils(
		r.Client,
		r.Cfg.LoadAverageStrategy.Namespace,
		r.Cfg.LoadAverageStrategy.PodLabel,
		r.Cfg.LoadAverageStrategy.Port,
		time.Duration(r.Cfg.LoadAverageStrategy.TimeoutSeconds)*time.Second,
	)
	evalMode := strategy.ParseClusterEvalMode(r.Cfg.LoadAverageStrategy.ClusterEval)

	// Try candidates until one passes both node and cluster checks.
	for _, cand := range eligible {
		slog.Debug("MaybeRotate: evaluating candidate", "node", cand.Name)

		// 1) Candidate node load check (normalized).
		var nodeLoad float64
		if r.DryRunNodeLoad != nil {
			nodeLoad = *r.DryRunNodeLoad
			slog.Info("MaybeRotate: dry-run node-load override in effect", "node", cand.Name, "load", nodeLoad)
		} else {
			val, err := utils.FetchNormalizedLoad(ctx, cand.Name)
			if err != nil {
				slog.Warn("MaybeRotate: failed to fetch node load, skipping candidate", "node", cand.Name, "err", err)
				continue
			}
			nodeLoad = val
		}

		if nodeLoad >= r.Cfg.LoadAverageStrategy.NodeThreshold {
			slog.Info("MaybeRotate: candidate load too high — skipping",
				"node", cand.Name, "load", nodeLoad, "threshold", r.Cfg.LoadAverageStrategy.NodeThreshold)
			continue
		}

		// 2) Cluster aggregate load check (exclude the candidate; exclude only disabled nodes from load math).
		agg, err := utils.GetClusterAggregateLoad(
			ctx,
			BuildAggregateExclusions(r.Cfg),
			cand.Name,               // exclude this candidate
			r.DryRunClusterLoadDown, // optional override for tests
			evalMode,
		)
		if err != nil {
			slog.Warn("MaybeRotate: failed to compute cluster aggregate load", "err", err)
			continue
		}

		slog.Info("MaybeRotate: cluster-wide load evaluation (rotation)",
			"aggregateLoad", agg,
			"clusterWideThreshold", r.Cfg.LoadAverageStrategy.ScaleDownThreshold,
			"evalMode", evalMode,
		)

		if agg >= r.Cfg.LoadAverageStrategy.ScaleDownThreshold {
			// If aggregate is too high with ANY candidate removed, further candidates are unlikely to help.
			slog.Info("MaybeRotate: cluster-wide load too high to rotate safely — aborting",
				"aggregateLoad", agg, "threshold", r.Cfg.LoadAverageStrategy.ScaleDownThreshold)
			return nil
		}

		// Candidate passes both checks.
		return cand
	}

	// None passed.
	return nil
}

// BuildAggregateExclusions returns the label set excluded from cluster-wide load math:
// union of disabled-label and loadAverageStrategy.excludeFromAggregateLabels.
func BuildAggregateExclusions(cfg *config.Config) map[string]string {
	ex := make(map[string]string, len(cfg.LoadAverageStrategy.ExcludeFromAggregateLabels)+1)
	if cfg.NodeLabels.Disabled != "" {
		ex[cfg.NodeLabels.Disabled] = "true"
	}
	for k, v := range cfg.LoadAverageStrategy.ExcludeFromAggregateLabels {
		ex[k] = v
	}
	return ex
}
