package power

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
)

func NewPowerControllerFromConfig(cfg *config.Config, client *kubernetes.Clientset) (PowerController, error) {
	switch cfg.ShutdownManager.ShutdownMode {
	case ShutdownModeDisabled:
		return &NoopPowerController{}, nil
	case ShutdownModeHTTP:
		return &ShutdownHTTPController{
			DryRun:    cfg.DryRun,
			Port:      cfg.ShutdownManager.Port,
			Namespace: cfg.ShutdownManager.Namespace,
			PodLabel:  cfg.ShutdownManager.PodLabel,
			Client:    client,
		}, nil
	default:
		return nil, fmt.Errorf("unknown shutdown mode: %s", cfg.ShutdownManager.ShutdownMode)
	}
}
