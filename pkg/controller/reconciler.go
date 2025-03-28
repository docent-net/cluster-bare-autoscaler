package controller

import (
	"context"
	"errors"

	policyv1 "k8s.io/api/policy/v1"
	"log/slog"
	"math/rand"
	"time"

	"go.opentelemetry.io/otel"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/metrics"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/power"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/strategy"
)

type Reconciler struct {
	cfg               *config.Config
	client            *kubernetes.Clientset
	power             power.PowerController
	state             *NodeStateTracker
	scaleDownStrategy strategy.ScaleDownStrategy
}

func NewReconciler(cfg *config.Config, client *kubernetes.Clientset) *Reconciler {
	return &Reconciler{
		cfg:    cfg,
		client: client,
		state:  NewNodeStateTracker(),
		power:  &power.LogPowerController{DryRun: cfg.DryRun},
		scaleDownStrategy: &strategy.ResourceAwareScaleDown{
			Client: client,
			Cfg:    cfg,
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
		},
	}
}

func (r *Reconciler) Reconcile(ctx context.Context) error {
	ctx, span := otel.Tracer("autoscaler").Start(ctx, "reconcile-loop")
	defer span.End()

	now := time.Now()
	if r.state.IsGlobalCooldownActive(now, r.cfg.Cooldown) {
		remaining := r.cfg.Cooldown - now.Sub(r.state.lastShutdownTime)
		slog.Info("Global cooldown active — skipping reconcile loop", "remaining", remaining.Round(time.Second).String())
		return nil
	}

	slog.Info("Running reconcile loop")
	metrics.Evaluations.Inc()

	allNodes, err := r.listAllNodes(ctx)
	if err != nil {
		return err
	}

	eligible := r.filterEligibleNodes(ctx, allNodes.Items)

	if r.maybeScaleDown(ctx, eligible) {
		return nil // stop here to avoid scaling up in the same loop
	}

	r.maybeScaleUp(ctx)
	return nil
}

func (r *Reconciler) filterEligibleNodes(ctx context.Context, nodes []v1.Node) []v1.Node {
	_, span := otel.Tracer("autoscaler").Start(ctx, "filter-eligible-nodes")
	defer span.End()

	eligible := r.getEligibleNodes(nodes)
	slog.Info("Filtered nodes", "eligible", len(eligible), "total", len(nodes))
	return eligible
}

func (r *Reconciler) listAllNodes(ctx context.Context) (*v1.NodeList, error) {
	spanCtx, span := otel.Tracer("autoscaler").Start(ctx, "list-nodes")
	defer span.End()

	nodes, err := r.client.CoreV1().Nodes().List(spanCtx, metav1.ListOptions{})
	if err != nil {
		slog.Error("failed to list nodes", "err", err)
		span.RecordError(err)
		return nil, err
	}
	return nodes, nil
}

func (r *Reconciler) maybeScaleUp(ctx context.Context) {
	if !r.shouldScaleUp(ctx) {
		return
	}

	for _, nodeCfg := range r.cfg.Nodes {
		if nodeCfg.Disabled {
			continue
		}
		if r.state.IsPoweredOff(nodeCfg.Name) {
			slog.Info("Attempting scale-up", "node", nodeCfg.Name)
			err := r.power.PowerOn(ctx, nodeCfg.Name)
			if err != nil {
				slog.Error("PowerOn failed", "node", nodeCfg.Name, "err", err)
			} else {
				slog.Info("Scale-up triggered", "node", nodeCfg.Name)
				r.state.ClearPoweredOff(nodeCfg.Name)
			}

			break // one per loop
		}
	}
}

func (r *Reconciler) maybeScaleDown(ctx context.Context, eligible []v1.Node) bool {
	candidate := r.pickScaleDownCandidate(eligible)
	if candidate == nil {
		slog.Info("No scale-down possible", "eligible", len(eligible), "minNodes", r.cfg.MinNodes)
		return false
	}

	ok, err := r.scaleDownStrategy.ShouldScaleDown(ctx, candidate.Name)
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

	{
		_, span := otel.Tracer("autoscaler").Start(ctx, "cordon-and-drain")
		defer span.End()

		if err := r.cordonAndDrain(ctx, candidate); err != nil {
			slog.Warn("cordonAndDrain failed", "node", candidate.Name, "err", err)
			return false
		}
	}

	{
		_, span := otel.Tracer("autoscaler").Start(ctx, "shutdown")
		defer span.End()

		metrics.ShutdownAttempts.Inc()
		if err := r.power.Shutdown(ctx, candidate.Name); err != nil {
			slog.Error("Shutdown failed", "node", candidate.Name, "err", err)
		} else {
			slog.Info("Shutdown initiated", "node", candidate.Name)
			metrics.ShutdownSuccesses.Inc()
			r.state.MarkGlobalShutdown()
		}
	}

	if !r.cfg.DryRun {
		r.state.MarkShutdown(candidate.Name)
		r.state.MarkPoweredOff(candidate.Name)
	}

	return true
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
			if node.Spec.Unschedulable {
				slog.Info("Skipping node because it is already cordoned", "node", node.Name)
				continue
			}

			if r.state.IsInCooldown(node.Name, time.Now(), r.cfg.Cooldown) {
				slog.Info("Skipping node due to cooldown", "node", node.Name)
				continue
			}

			if r.state.IsPoweredOff(node.Name) {
				slog.Info("Skipping node: already powered off", "node", node.Name)
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

func (r *Reconciler) shouldScaleUp(ctx context.Context) bool {
	return true // Always scale up — will replace with real strategy later
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
