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
			IgnoreLabels:              cfg.IgnoreLabels,
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
			IgnoreLabels:         map[string]string{cfg.NodeLabels.Disabled: "true"}, // changed
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

// MaybeRotate performs a maintenance rotation when no scale up/down happened in this loop.
// Flow:
// 1) pick the oldest powered-off node whose off-age >= Rotation.MaxPoweredOffDuration (respect exempt & ignore labels)
// 2) ensure there is an eligible active node we could power off (respect minNodes, cooldowns, ignore/disabled)
// 3) if LoadAverage is enabled, ensure candidate node load < NodeThreshold AND cluster aggregate load (excluding candidate) < ScaleDownThreshold
// 4) power-on the overdue node first (wait for Ready), clear its powered-off state/metrics
// 5) re-list eligible nodes and shut down exactly one candidate
// If power-on fails, abort the rotation without shutting anything down.
func (r *Reconciler) MaybeRotate(ctx context.Context) {
	if r.Cfg == nil || !r.Cfg.Rotation.Enabled || r.Cfg.Rotation.MaxPoweredOffDuration <= 0 {
		return
	}

	// 1) discover the oldest overdue powered-off node
	managed, err := nodeops.ListManagedNodes(ctx, r.Client, nodeops.ManagedNodeFilter{
		ManagedLabel:  r.Cfg.NodeLabels.Managed,
		DisabledLabel: r.Cfg.NodeLabels.Disabled,
		IgnoreLabels:  r.Cfg.IgnoreLabels,
	})
	if err != nil || len(managed) == 0 {
		if err != nil {
			slog.Warn("rotation: listing managed nodes failed", "err", err)
		}
		return
	}

	now := time.Now().UTC()
	var (
		overdue *v1.Node
		since   time.Time
	)
	for i := range managed {
		n := managed[i]

		// per-node exemption
		if key := r.Cfg.Rotation.ExemptLabel; key != "" {
			if val, ok := n.Labels[key]; ok && val != "" {
				continue
			}
		}
		// honor global ignore labels
		if nodeops.ShouldIgnoreNodeDueToLabels(n, r.Cfg.IgnoreLabels) {
			continue
		}

		if t, ok := nodeops.PoweredOffSince(n); ok {
			age := now.Sub(t)
			if age >= r.Cfg.Rotation.MaxPoweredOffDuration {
				if overdue == nil || t.Before(since) {
					overdue = &managed[i]
					since = t
				}
			}
		}
	}
	if overdue == nil {
		// nothing overdue
		return
	}

	// 2) ensure we have an eligible active node we could power off
	allNodes, err := r.listAllNodes(ctx)
	if err != nil {
		slog.Warn("rotation: failed to list nodes", "err", err)
		return
	}
	eligible := r.filterEligibleNodes(allNodes.Items)
	if len(eligible) <= r.Cfg.MinNodes {
		slog.Info("rotation: skip — eligible nodes at/below minNodes",
			"eligible", len(eligible), "minNodes", r.Cfg.MinNodes)
		return
	}

	// 3) if LoadAverage is enabled, enforce the same gates as scale-down
	// Candidate selection also applies LA checks when enabled.
	cand := r.PickRotationPoweroffCandidate(ctx, eligible)
	if cand == nil {
		slog.Info("rotation: skip — no suitable power-off candidate (gates or eligibility)")
		return
	}

	slog.Info("rotation: powering on overdue node",
		"node", overdue.Name, "poweredOffSince", since)

	// 4) power-on first to keep capacity steady; wrapper will clear annotation & uncordon
	wrapped := nodeops.NewNodeWrapper(overdue, r.State, now, nodeops.NodeAnnotationConfig{
		MAC: r.Cfg.NodeAnnotations.MAC,
	}, r.Cfg.IgnoreLabels)

	if err := nodeops.PowerOnAndMarkBooted(ctx, wrapped, r.Cfg, r.Client, r.PowerOner, r.State, r.Cfg.DryRun); err != nil {
		slog.Warn("rotation: power-on failed; aborting rotation", "node", overdue.Name, "err", err)
		return
	}
	// clear powered-off state/metric like in scale-up
	r.State.ClearPoweredOff(overdue.Name)
	metrics.PoweredOffNodes.WithLabelValues(overdue.Name).Set(0)

	// 5) re-evaluate candidates (freshly booted node will be excluded by boot cooldown)
	allAfter, err := r.listAllNodes(ctx)
	if err != nil {
		slog.Warn("rotation: failed to list nodes post power-on", "err", err)
		return
	}
	eligible = r.filterEligibleNodes(allAfter.Items)
	if len(eligible) <= r.Cfg.MinNodes {
		slog.Info("rotation: no longer safe to retire any node after power-on",
			"eligible", len(eligible), "minNodes", r.Cfg.MinNodes)
		return
	}
	cand = r.PickRotationPoweroffCandidate(ctx, eligible)
	if cand == nil {
		slog.Info("rotation: no suitable power-off candidate after power-on; leaving extra node online")
		return
	}

	slog.Info("rotation: retiring active node", "rotatedIn", overdue.Name, "rotatedOut", cand.Name)

	if err := r.CordonAndDrain(ctx, cand); err != nil {
		slog.Warn("rotation: drain failed; keeping both nodes online", "node", cand.Name, "err", err)
		_ = nodeops.ClearPoweredOffAnnotation(ctx, r.Client, cand.Name) // best-effort cleanup
		return
	}

	if err := r.AnnotatePoweredOffNode(ctx, cand); err != nil {
		slog.Warn("rotation: failed to annotate powered-off node", "node", cand.Name, "err", err)
	}

	metrics.ShutdownAttempts.Inc()
	if err := r.Shutdowner.Shutdown(ctx, cand.Name); err != nil {
		slog.Error("rotation: shutdown failed", "node", cand.Name, "err", err)
		if err := nodeops.ClearPoweredOffAnnotation(ctx, r.Client, cand.Name); err != nil {
			slog.Warn("rotation: failed to clear annotation after shutdown failure", "node", cand.Name, "err", err)
		}
		return
	}

	slog.Info("rotation: shutdown initiated", "node", cand.Name)
	metrics.ShutdownSuccesses.Inc()
	metrics.PoweredOffNodes.WithLabelValues(cand.Name).Set(1)
	r.State.MarkGlobalShutdown()

	if !r.Cfg.DryRun {
		r.State.MarkShutdown(cand.Name)
		r.State.MarkPoweredOff(cand.Name)
	}
}

// PickRotationPoweroffCandidate applies optional LoadAverage checks to find a safe node to power off.
// - Skips the node we just booted (excludeJustBooted).
// - If LoadAverage is disabled, returns the same candidate as PickScaleDownCandidate().
// - If enabled, requires:
//   - candidate normalized load < NodeThreshold
//   - cluster aggregate load (excluding candidate) < ScaleDownThreshold
func (r *Reconciler) PickRotationPoweroffCandidate(ctx context.Context, eligible []*nodeops.NodeWrapper) *nodeops.NodeWrapper {
	// If LoadAverage strategy is disabled, keep existing selection behavior.
	if !r.Cfg.LoadAverageStrategy.Enabled {
		return r.PickScaleDownCandidate(eligible)
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
			slog.Info("MaybeRotate: candidate load too high — skipping", "node", cand.Name, "load", nodeLoad, "threshold", r.Cfg.LoadAverageStrategy.NodeThreshold)
			continue
		}

		// 2) Cluster aggregate load check (exclude the candidate).
		agg, err := utils.GetClusterAggregateLoad(
			ctx,
			map[string]string{r.Cfg.NodeLabels.Disabled: "true"}, // changed: exclude only disabled
			cand.Name,
			r.DryRunClusterLoadDown,
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
			// If aggregate is too high with ANY candidate removed, further candidates are unlikely to change the outcome meaningfully.
			slog.Info("MaybeRotate: cluster-wide load too high to rotate safely — aborting", "aggregateLoad", agg, "threshold", r.Cfg.LoadAverageStrategy.ScaleDownThreshold)
			return nil
		}

		// Candidate passes both checks.
		return cand
	}

	// None passed.
	return nil
}
