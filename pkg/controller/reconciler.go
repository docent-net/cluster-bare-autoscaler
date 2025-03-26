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

	nodes, err := r.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	eligibleNodes := []v1.Node{}
	for _, node := range nodes.Items {
		skip := false
		for key, val := range r.cfg.IgnoreLabels {
			if nodeVal, ok := node.Labels[key]; ok && nodeVal == val {
				slog.Info("Skipping node due to ignoreLabels", "node", node.Name, "label", key)
				skip = true
				break
			}
		}
		if !skip {
			eligibleNodes = append(eligibleNodes, node)
		}
	}

	slog.Info("Filtered nodes", "eligible", len(eligibleNodes), "total", len(nodes.Items))
	// TODO: Evaluate if any nodes can be safely shut down

	return nil
}
