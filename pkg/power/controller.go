package power

import (
	"context"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"k8s.io/client-go/kubernetes"
	"log/slog"
)

const (
	ShutdownModeDisabled = "disabled"
	ShutdownModeHTTP     = "http"
	ShutdownModeNOOP     = "noop"
)

type PowerController interface {
	Shutdown(ctx context.Context, nodeName string) error
	PowerOn(ctx context.Context, nodeName string) error
}

func NewPowerControllerFromConfig(cfg *config.Config, client *kubernetes.Clientset) PowerController {
	slog.Debug("Using configured shutdown mode", "mode", cfg.ShutdownMode)

	switch cfg.ShutdownMode {
	case ShutdownModeDisabled:
		return &NoopPowerController{}
	case ShutdownModeHTTP:
		return &ShutdownHTTPController{
			DryRun:    cfg.DryRun,
			Port:      cfg.ShutdownManager.Port,
			Namespace: cfg.ShutdownManager.Namespace,
			PodLabel:  cfg.ShutdownManager.PodLabel,
			Client:    client,
		}
	default:
		slog.Warn("Unknown shutdown mode; falling back to default", "mode", cfg.ShutdownMode, "fallback", ShutdownModeNOOP)
		return &NoopPowerController{}
	}
}
