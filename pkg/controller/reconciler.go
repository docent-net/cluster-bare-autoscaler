package controller

import (
	"context"
	"log/slog"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
)

type Reconciler struct {
	cfg    *config.Config
	client *kubernetes.Clientset
}

func NewReconciler(cfg *config.Config, client *kubernetes.Clientset) *Reconciler {
	return &Reconciler{cfg: cfg, client: client}
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

	if err := r.cordonAndDrainDryRun(ctx, candidate); err != nil {
		slog.Error("cordonAndDrainDryRun failed", "err", err)
	}

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

func (r *Reconciler) cordonAndDrainDryRun(ctx context.Context, node *v1.Node) error {
	slog.Info("Dry-run: cordon node", "node", node.Name)

	// List all pods on the node
	pods, err := r.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + node.Name,
	})
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		// Skip mirror pods (static pods)
		if _, ok := pod.Annotations["kubernetes.io/config.mirror"]; ok {
			slog.Info("Skipping mirror pod", "pod", pod.Name, "ns", pod.Namespace)
			continue
		}
		// Skip DaemonSet pods
		if controllerRef := metav1.GetControllerOf(&pod); controllerRef != nil && controllerRef.Kind == "DaemonSet" {
			slog.Info("Skipping DaemonSet pod", "pod", pod.Name, "ns", pod.Namespace)
			continue
		}

		// Log pod that would be evicted
		slog.Info("Dry-run: evict pod", "pod", pod.Name, "ns", pod.Namespace)
	}

	slog.Info("Dry-run: node would be drained", "node", node.Name)
	return nil
}
