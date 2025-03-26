package power

import (
	"context"
	"log/slog"
)

type PowerController interface {
	Shutdown(ctx context.Context, nodeName string) error
}

type LogPowerController struct{}

func (l *LogPowerController) Shutdown(ctx context.Context, nodeName string) error {
	slog.Info("PowerController: simulated shutdown", "node", nodeName)
	return nil
}
