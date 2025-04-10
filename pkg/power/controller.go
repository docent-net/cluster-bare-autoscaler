package power

import (
	"context"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	"k8s.io/client-go/kubernetes"
	"log/slog"
	"time"
)

const (
	ShutdownModeDisabled = "disabled"
	ShutdownModeHTTP     = "http"
)

const (
	PowerOnModeDisabled = "disabled"
	PowerOnModeWOL      = "wol"
)

type PowerOnController interface {
	PowerOn(ctx context.Context, nodeName string) error
}

type ShutdownController interface {
	Shutdown(ctx context.Context, nodeName string) error
}

func NewControllersFromConfig(cfg *config.Config, client *kubernetes.Clientset) (ShutdownController, PowerOnController) {
	var shutdowner ShutdownController
	switch cfg.ShutdownMode {
	case ShutdownModeDisabled:
		shutdowner = &NoopShutdownController{}
	case ShutdownModeHTTP:
		shutdowner = &ShutdownHTTPController{
			DryRun:    cfg.DryRun,
			Port:      cfg.ShutdownManager.Port,
			Namespace: cfg.ShutdownManager.Namespace,
			PodLabel:  cfg.ShutdownManager.PodLabel,
			Client:    client,
		}
	default:
		slog.Warn("Unknown shutdown mode; falling back to", "mode", ShutdownModeDisabled)
		shutdowner = &NoopShutdownController{}
	}

	var powerOner PowerOnController
	switch cfg.PowerOnMode {
	case PowerOnModeDisabled:
		powerOner = &NoopPowerOnController{}
	case PowerOnModeWOL:
		powerOner = &WakeOnLanController{
			DryRun: cfg.DryRun,
			NodeFilter: nodeops.ManagedNodeFilter{
				ManagedLabel:  cfg.NodeLabels.Managed,
				DisabledLabel: cfg.NodeLabels.Disabled,
				IgnoreLabels:  cfg.IgnoreLabels,
			},
			MACKey: nodeops.NodeAnnotationConfig{
				MAC: cfg.NodeAnnotations.MAC,
			},
			BroadcastAddr:  cfg.WOLBroadcastAddr,
			BootTimeoutSec: time.Duration(cfg.WOLBootTimeoutSec) * time.Second,
			Client:         client,
			MaxRetries:     3,
			Namespace:      cfg.WolAgent.Namespace,
			PodLabel:       cfg.WolAgent.PodLabel,
			Port:           cfg.WolAgent.Port,
		}
	default:
		slog.Warn("Unknown power-on mode; falling back to", "mode", PowerOnModeDisabled)
		powerOner = &NoopPowerOnController{}
	}

	slog.Debug("Using configured shutdown mode", "mode", cfg.ShutdownMode)
	slog.Debug("Using configured power-on mode", "mode", cfg.PowerOnMode)

	return shutdowner, powerOner
}
