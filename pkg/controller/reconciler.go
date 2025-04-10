package controller

import (
	"context"
	"errors"
	"fmt"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	policyv1 "k8s.io/api/policy/v1"
	"log/slog"
	"math/rand"
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

const annotationPoweredOff = "cba.dev/was-powered-off"

type Reconciler struct {
	cfg                   *config.Config
	client                *kubernetes.Clientset
	shutdowner            power.ShutdownController
	powerOner             power.PowerOnController
	state                 *nodeops.NodeStateTracker
	scaleDownStrategy     strategy.ScaleDownStrategy
	scaleUpStrategy       strategy.ScaleUpStrategy
	dryRunNodeLoad        *float64 // optional CLI override
	dryRunClusterLoadDown *float64 // CLI override for scale-down
	dryRunClusterLoadUp   *float64 // CLI override for scale-up
}

type ReconcilerOption func(r *Reconciler)

func NewReconciler(cfg *config.Config, client *kubernetes.Clientset, metricsClient metricsclient.Interface, opts ...ReconcilerOption) *Reconciler {
	shutdowner, powerOner := power.NewControllersFromConfig(cfg, client)
	r := &Reconciler{
		cfg:        cfg,
		client:     client,
		state:      nodeops.NewNodeStateTracker(),
		shutdowner: shutdowner,
		powerOner:  powerOner,
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	r.scaleDownStrategy = buildScaleDownStrategy(cfg, client, metricsClient, r)
	r.scaleUpStrategy = buildScaleUpStrategy(cfg, r)

	r.restorePoweredOffState(context.Background())
	return r
}

// buildScaleDownStrategy constructs a composite scale-down strategy based on the current config.
// It includes the ResourceAwareScaleDown strategy by default to ensure pods can fit elsewhere.
// If LoadAverageStrategy is enabled, it adds LoadAverageScaleDown, which shuts down nodes
// based on normalized per-node and cluster-wide load averages.
// Supports dry-run overrides and evaluates multiple strategies using a MultiStrategy chain.
func buildScaleDownStrategy(cfg *config.Config, client *kubernetes.Clientset, metricsClient metricsclient.Interface, r *Reconciler) strategy.ScaleDownStrategy {
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
			DryRunNodeLoadOverride:    r.dryRunNodeLoad,
			DryRunClusterLoadOverride: r.dryRunClusterLoadDown,
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
			Cfg:          r.cfg,
			ActiveNodes:  r.listActiveNodes,
			ShutdownList: r.shutdownNodeNames,
		},
	}

	if cfg.LoadAverageStrategy.Enabled {
		upStrategies = append(upStrategies, &strategy.LoadAverageScaleUp{
			Client:               r.client,
			Namespace:            cfg.LoadAverageStrategy.Namespace,
			PodLabel:             cfg.LoadAverageStrategy.PodLabel,
			HTTPPort:             cfg.LoadAverageStrategy.Port,
			HTTPTimeout:          time.Duration(cfg.LoadAverageStrategy.TimeoutSeconds) * time.Second,
			ClusterEvalMode:      strategy.ParseClusterEvalMode(cfg.LoadAverageStrategy.ClusterEval),
			ClusterWideThreshold: cfg.LoadAverageStrategy.ScaleUpThreshold,
			DryRunOverride:       r.dryRunClusterLoadUp,
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
	if r.state.IsGlobalCooldownActive(now, r.cfg.Cooldown) {
		remaining := r.cfg.Cooldown - now.Sub(r.state.LastShutdownTime)
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
	r.maybeScaleDown(ctx, eligible)

	return nil
}

func (r *Reconciler) restorePoweredOffState(ctx context.Context) {
	nodeList, err := r.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Warn("Failed to list nodes while restoring powered-off state", "err", err)
		return
	}

	// Build a map of currently running nodes
	active := make(map[string]struct{})
	for _, node := range nodeList.Items {
		active[node.Name] = struct{}{}
	}

	managed, err := nodeops.ListManagedNodes(ctx, r.client, nodeops.ManagedNodeFilter{
		ManagedLabel:  r.cfg.NodeLabels.Managed,
		DisabledLabel: r.cfg.NodeLabels.Disabled,
		IgnoreLabels:  r.cfg.IgnoreLabels,
	})
	if err != nil {
		slog.Warn("Failed to list managed nodes during restore", "err", err)
		return
	}
	for _, node := range managed {
		if _, found := active[node.Name]; !found {
			slog.Info("Managed node not found in active set — assuming powered off", "node", node.Name)
			r.state.MarkPoweredOff(node.Name)
		}
	}
}

func (r *Reconciler) filterEligibleNodes(ctx context.Context, nodes []v1.Node) []v1.Node {
	eligible := r.getEligibleNodes(nodes)
	slog.Info("Filtered nodes", "eligible", len(eligible), "total", len(nodes))
	return eligible
}

func (r *Reconciler) listAllNodes(ctx context.Context) (*v1.NodeList, error) {
	nodes, err := r.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("failed to list nodes", "err", err)
		return nil, err
	}
	return nodes, nil
}

func (r *Reconciler) listActiveNodes(ctx context.Context) ([]v1.Node, error) {
	nodes, err := r.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var active []v1.Node
	for _, node := range nodes.Items {
		// Skip cordoned nodes
		if node.Spec.Unschedulable {
			continue
		}

		// Skip ignored labels
		skip := false
		for k, v := range r.cfg.IgnoreLabels {
			if nodeVal, ok := node.Labels[k]; ok && nodeVal == v {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		// Skip nodes with annotationPoweredOff
		if val, ok := node.Annotations[annotationPoweredOff]; ok && val == "true" {
			continue
		}

		// Skip nodes marked as powered-off in state tracker
		if r.state.IsPoweredOff(node.Name) {
			continue
		}

		// Must be Ready
		for _, cond := range node.Status.Conditions {
			if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
				active = append(active, node)
				break
			}
		}
	}

	return active, nil
}

func (r *Reconciler) shutdownNodeNames(ctx context.Context) []string {
	nodes, err := nodeops.ListShutdownNodeNames(ctx, r.client, nodeops.ManagedNodeFilter{
		ManagedLabel:  r.cfg.NodeLabels.Managed,
		DisabledLabel: r.cfg.NodeLabels.Disabled,
		IgnoreLabels:  r.cfg.IgnoreLabels,
	}, r.state)

	if err != nil {
		slog.Warn("Failed to list shutdown nodes", "err", err)
		return nil
	}
	return nodes
}

func (r *Reconciler) maybeScaleUp(ctx context.Context) bool {
	nodeName, shouldScale, err := r.scaleUpStrategy.ShouldScaleUp(ctx)
	if err != nil {
		slog.Error("Scale-up strategy error", "err", err)
		return false
	}
	if !shouldScale {
		slog.Info("No scale-up possible", "reason", "all strategies denied", "minNodes", r.cfg.MinNodes)
		return false
	}

	slog.Info("Attempting scale-up", "node", nodeName)
	err = r.powerOner.PowerOn(ctx, nodeName)
	if err != nil {
		slog.Error("PowerOn failed", "node", nodeName, "err", err)
		return false
	}

	slog.Info("Scale-up triggered", "node", nodeName)
	r.state.ClearPoweredOff(nodeName)
	metrics.PoweredOffNodes.WithLabelValues(nodeName).Set(0)

	// Uncordon node
	if err := r.uncordonNode(ctx, nodeName); err != nil {
		slog.Warn("Failed to uncordon node after power-on", "node", nodeName, "err", err)
		return false
	}

	// Clear powered-off annotation
	if err := r.clearPoweredOffAnnotation(ctx, nodeName); err != nil {
		slog.Warn("Failed to clear powered-off annotation", "node", nodeName, "err", err)
	}

	r.state.MarkGlobalShutdown()
	r.state.MarkBooted(nodeName)

	return true // scale-up action performed
}

func (r *Reconciler) uncordonNode(ctx context.Context, nodeName string) error {
	node, err := r.client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node for uncordon: %w", err)
	}

	if !node.Spec.Unschedulable {
		slog.Debug("Node is already schedulable", "node", nodeName)
		return nil
	}

	updated := node.DeepCopy()
	updated.Spec.Unschedulable = false

	if r.cfg.DryRun {
		slog.Debug("Dry-run: would uncordon node", "node", nodeName)
		return nil
	}

	_, err = r.client.CoreV1().Nodes().Update(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to uncordon node: %w", err)
	}

	slog.Info("Node uncordoned successfully", "node", nodeName)
	return nil
}

func (r *Reconciler) maybeScaleDown(ctx context.Context, eligible []v1.Node) bool {
	candidate := r.pickScaleDownCandidate(eligible)
	if candidate == nil {
		slog.Info("No scale-down possible", "eligible", len(eligible), "minNodes", r.cfg.MinNodes)
		return false
	}

	ok, err := r.scaleDownStrategy.
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

	if err := r.annotatePoweredOffNode(ctx, candidate.Name); err != nil {
		slog.Warn("Failed to annotate powered-off node", "node", candidate.Name, "err", err)
	}

	metrics.ShutdownAttempts.Inc()
	if err := r.shutdowner.Shutdown(ctx, candidate.Name); err != nil {
		slog.Error("Shutdown failed", "node", candidate.Name, "err", err)
		if err := r.clearPoweredOffAnnotation(ctx, candidate.Name); err != nil {
			slog.Warn("Failed to clear annotation from powered-off node", "node", candidate.Name, "err", err)
		}
	} else {
		slog.Info("Shutdown initiated", "node", candidate.Name)
		metrics.ShutdownSuccesses.Inc()
		metrics.PoweredOffNodes.WithLabelValues(candidate.Name).Set(1)
		r.state.MarkGlobalShutdown()
	}

	if !r.cfg.DryRun {
		r.state.MarkShutdown(candidate.Name)
		r.state.MarkPoweredOff(candidate.Name)
	}

	return true
}

func (r *Reconciler) annotatePoweredOffNode(ctx context.Context, nodeName string) error {
	if r.cfg.DryRun {
		slog.Debug("Dry-run: would annotate node as powered-off", "node", nodeName)
		return nil
	}
	slog.Debug("Annotating node as powered-off", "node", nodeName)
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"true"}}}`, annotationPoweredOff))
	_, err := r.client.CoreV1().Nodes().Patch(ctx, nodeName, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *Reconciler) clearPoweredOffAnnotation(ctx context.Context, nodeName string) error {
	if r.cfg.DryRun {
		slog.Debug("Dry-run: would clear powered-off annotation", "node", nodeName)
		return nil
	}
	slog.Debug("Clearing powered-off annotation", "node", nodeName)
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, annotationPoweredOff))
	_, err := r.client.CoreV1().Nodes().Patch(ctx, nodeName, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *Reconciler) getEligibleNodes(all []v1.Node) []v1.Node {
	var eligible []v1.Node
	for _, node := range all {
		skip := false
		for key, val := range r.cfg.IgnoreLabels {
			if nodeVal, ok := node.Labels[key]; ok && nodeVal == val {
				slog.Info("Skipping node due to ignoreLabels", "node", node.Name, "label", key)
				skip = true
				break
			}
		}
		if !skip {
			if val, ok := node.Annotations[annotationPoweredOff]; ok && val == "true" {
				slog.Info("Skipping node marked as powered-off (annotation)", "node", node.Name)
				continue
			}

			if node.Spec.Unschedulable {
				slog.Info("Skipping node because it is already cordoned", "node", node.Name)
				continue
			}

			now := time.Now()
			if r.state.IsInCooldown(node.Name, now, r.cfg.Cooldown) {
				slog.Info("Skipping node due to shutdown cooldown", "node", node.Name)
				continue
			}
			if r.state.IsBootCooldownActive(node.Name, now, r.cfg.BootCooldown) {
				slog.Info("Skipping node due to boot cooldown", "node", node.Name)
				continue
			}

			if r.state.IsPoweredOff(node.Name) {
				slog.Info("Skipping node: already powered off", "node", node.Name)
				continue
			}

			if r.state.IsBootCooldownActive(node.Name, time.Now(), r.cfg.BootCooldown) {
				slog.Info("Skipping node due to boot cooldown", "node", node.Name)
				continue
			}

			eligible = append(eligible, node)
		}
	}

	// Shuffle to avoid always picking the same node
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(eligible), func(i, j int) {
		eligible[i], eligible[j] = eligible[j], eligible[i]
	})

	return eligible
}

func (r *Reconciler) pickScaleDownCandidate(eligible []v1.Node) *v1.Node {
	if len(eligible) <= r.cfg.MinNodes {
		return nil
	}
	return &eligible[len(eligible)-1]
}

func (r *Reconciler) cordonAndDrain(ctx context.Context, node *v1.Node) error {
	// Step 1: Cordon
	latest, err := r.client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		slog.Error("failed to refetch node before cordon", "node", node.Name, "err", err)
		return err
	}

	latestCopy := latest.DeepCopy()
	latestCopy.Spec.Unschedulable = true

	if r.cfg.DryRun {
		slog.Info("Dry-run: would cordon node", "node", node.Name)
	} else {
		_, err = r.client.CoreV1().Nodes().Update(ctx, latestCopy, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		slog.Info("Node cordoned", "node", node.Name)
	}

	// Step 2: List pods on node
	pods, err := r.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
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

		if r.cfg.DryRun {
			slog.Info("Dry-run: would evict pod", "pod", pod.Name, "ns", pod.Namespace)
		} else {
			err := r.client.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
			if err != nil {
				slog.Warn("Eviction failed", "pod", pod.Name, "err", err)
				metrics.EvictionFailures.Inc()
				return errors.New("aborting drain due to eviction failure")
			}
			slog.Info("Evicted pod", "pod", pod.Name, "ns", pod.Namespace)
		}
	}

	slog.Info("Node drained successfully", "node", node.Name)
	return nil
}
