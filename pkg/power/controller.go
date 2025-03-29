package power

import (
	"context"
	"log/slog"
)

type PowerController interface {
	Shutdown(ctx context.Context, nodeName string) error
	PowerOn(ctx context.Context, nodeName string) error
}

type LogPowerController struct {
	DryRun bool
}

func (l *LogPowerController) PowerOn(ctx context.Context, node string) error {
	if l.DryRun {
		slog.Info("Dry-run: would power on", "node", node)
		return nil
	}
	slog.Info("Powering on", "node", node)
	return nil
}

func (l *LogPowerController) Shutdown(ctx context.Context, node string) error {
	if l.DryRun {
		slog.Info("Dry-run: would shut down", "node", node)
		return nil
	}
	slog.Info("Shutting down", "node", node)
	return nil
}
