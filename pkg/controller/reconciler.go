package controller

import (
	"context"
	"log/slog"

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
	// TODO: List nodes, apply strategies, decide if scale-down
	return nil
}
