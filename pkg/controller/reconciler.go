package controller

import (
	"context"
	"errors"
	"fmt"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	policyv1 "k8s.io/api/policy/v1"
	"log/slog"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/metrics"
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

	r.restorePoweredOffState(context.Background())
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
			IgnoreLabels:         cfg.IgnoreLabels,
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
	if r.State.IsGlobalCooldownActive(now, r.Cfg.Cooldown) {
		remaining := r.Cfg.Cooldown - now.Sub(r.State.LastShutdownTime)
		slog.Info("Global cooldown active — skipping reconcile loop", "remaining", remaining.Round(time.Second).String())
		return nil
	}

	slog.Info("Running reconcile loop")
	metrics.Evaluations.Inc()

	if r.maybeScaleUp(ctx) {
		return nil // stop here to avoid scaling up in the same loop
	}

	allNodes, err := r.listAllNodes(ctx)
	if err != nil {
		return err
	}

	eligible := r.filterEligibleNodes(ctx, allNodes.Items)
	r.MaybeScaleDown(ctx, eligible)

	return nil
}

func (r *Reconciler) restorePoweredOffState(ctx context.Context) {
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

func (r *Reconciler) filterEligibleNodes(ctx context.Context, nodes []v1.Node) []*nodeops.NodeWrapper {
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

func (r *Reconciler) maybeScaleUp(ctx context.Context) bool {
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
	err = r.PowerOner.PowerOn(ctx, nodeName)
	if err != nil {
		slog.Error("PowerOn failed", "node", nodeName, "err", err)
		return false
	}

	slog.Info("Scale-up triggered", "node", nodeName)
	r.State.ClearPoweredOff(nodeName)
	metrics.PoweredOffNodes.WithLabelValues(nodeName).Set(0)

	// Uncordon node
	if err := r.UncordonNode(ctx, nodeName); err != nil {
		slog.Warn("Failed to uncordon node after power-on", "node", nodeName, "err", err)
		return false
	}

	// Clear powered-off annotation
	if err := r.clearPoweredOffAnnotation(ctx, nodeName); err != nil {
		slog.Warn("Failed to clear powered-off annotation", "node", nodeName, "err", err)
	}

	r.State.MarkGlobalShutdown()
	r.State.MarkBooted(nodeName)

	return true // scale-up action performed
}

func (r *Reconciler) UncordonNode(ctx context.Context, nodeName string) error {
	node, err := r.Client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node for uncordon: %w", err)
	}

	if !node.Spec.Unschedulable {
		slog.Debug("Node is already schedulable", "node", nodeName)
		return nil
	}

	updated := node.DeepCopy()
	updated.Spec.Unschedulable = false

	if r.Cfg.DryRun {
		slog.Debug("Dry-run: would uncordon node", "node", nodeName)
		return nil
	}

	_, err = r.Client.CoreV1().Nodes().Update(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to uncordon node: %w", err)
	}

	slog.Info("Node uncordoned successfully", "node", nodeName)
	return nil
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

	if err := r.cordonAndDrain(ctx, candidate); err != nil {
		slog.Warn("cordonAndDrain failed", "node", candidate.Name, "err", err)
		if err := r.clearPoweredOffAnnotation(ctx, candidate.Name); err != nil {
			slog.Warn("Failed to clear annotation from powered-off node", "node", candidate.Name, "err", err)
		}
		return false
	}

	if err := r.annotatePoweredOffNode(ctx, candidate); err != nil {
		slog.Warn("Failed to annotate powered-off node", "node", candidate.Name, "err", err)
	}

	metrics.ShutdownAttempts.Inc()
	if err := r.Shutdowner.Shutdown(ctx, candidate.Name); err != nil {
		slog.Error("Shutdown failed", "node", candidate.Name, "err", err)
		if err := r.clearPoweredOffAnnotation(ctx, candidate.Name); err != nil {
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

func (r *Reconciler) annotatePoweredOffNode(ctx context.Context, node *nodeops.NodeWrapper) error {
	if r.Cfg.DryRun {
		slog.Debug("Dry-run: would annotate node as powered-off", "node", node.Name)
		return nil
	}
	slog.Debug("Annotating node as powered-off", "node", node.Name)
	timestamp := metav1.Now().UTC().Format("2006-01-02T15:04:05Z")
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, nodeops.AnnotationPoweredOff, timestamp))
	_, err := r.Client.CoreV1().Nodes().Patch(ctx, node.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *Reconciler) clearPoweredOffAnnotation(ctx context.Context, nodeName string) error {
	if r.Cfg.DryRun {
		slog.Debug("Dry-run: would clear powered-off annotation", "node", nodeName)
		return nil
	}
	slog.Debug("Clearing powered-off annotation", "node", nodeName)
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, nodeops.AnnotationPoweredOff))
	_, err := r.Client.CoreV1().Nodes().Patch(ctx, nodeName, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *Reconciler) PickScaleDownCandidate(eligible []*nodeops.NodeWrapper) *nodeops.NodeWrapper {
	if len(eligible) <= r.Cfg.MinNodes {
		return nil
	}
	return eligible[len(eligible)-1]
}

func (r *Reconciler) cordonAndDrain(ctx context.Context, node *nodeops.NodeWrapper) error {
	// Step 1: Cordon
	latest, err := r.Client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		slog.Error("failed to refetch node before cordon", "node", node.Name, "err", err)
		return err
	}

	latestCopy := latest.DeepCopy()
	latestCopy.Spec.Unschedulable = true

	if r.Cfg.DryRun {
		slog.Info("Dry-run: would cordon node", "node", node.Name)
	} else {
		_, err = r.Client.CoreV1().Nodes().Update(ctx, latestCopy, metav1.UpdateOptions{})
		if err != nil {
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
