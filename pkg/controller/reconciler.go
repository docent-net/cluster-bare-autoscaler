package controller

import (
	"context"
	"errors"
	policyv1 "k8s.io/api/policy/v1"
	"log/slog"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/power"
)

type Reconciler struct {
	cfg    *config.Config
	client *kubernetes.Clientset
	power  power.PowerController
	state  *NodeStateTracker
}

func NewReconciler(cfg *config.Config, client *kubernetes.Clientset) *Reconciler {
	return &Reconciler{
		cfg:    cfg,
		client: client,
		power:  &power.LogPowerController{},
		state:  NewNodeStateTracker(),
	}
}

func (r *Reconciler) Reconcile(ctx context.Context) error {
	slog.Info("Running reconcile loop")

	allNodes, err := r.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	eligible := r.getEligibleNodes(allNodes.Items)
	slog.Info("Filtered nodes", "eligible", len(eligible), "total", len(allNodes.Items))

	candidate := r.pickScaleDownCandidate(eligible)
	if candidate == nil {
		slog.Info("No scale-down possible", "eligible", len(eligible), "minNodes", r.cfg.MinNodes)
		return nil
	}

	slog.Info("Candidate for scale-down", "node", candidate.Name)

	if err := r.cordonAndDrain(ctx, candidate); err != nil {
		slog.Warn("cordonAndDrain failed", "node", candidate.Name, "err", err)
		return nil
	}

	if err := r.power.Shutdown(ctx, candidate.Name); err != nil {
		slog.Error("Shutdown failed", "node", candidate.Name, "err", err)
	} else {
		slog.Info("Shutdown initiated", "node", candidate.Name)
	}

	r.state.MarkShutdown(candidate.Name)

	return nil
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

			eligible = append(eligible, node)
		}
	}
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
	nodeCopy := node.DeepCopy()
	nodeCopy.Spec.Unschedulable = true

	_, err := r.client.CoreV1().Nodes().Update(ctx, nodeCopy, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	slog.Info("Node cordoned", "node", node.Name)

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

		err := r.client.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
		if err != nil {
			slog.Warn("Eviction failed", "pod", pod.Name, "err", err)
			return errors.New("aborting drain due to eviction failure")
		}

		slog.Info("Evicted pod", "pod", pod.Name, "ns", pod.Namespace)
	}

	slog.Info("Node drained successfully", "node", node.Name)
	return nil
}
