package power

import (
	"context"
	"log/slog"
)

type NoopPowerController struct{}

func (n *NoopPowerController) Shutdown(ctx context.Context, node string) error {
	slog.Info("Shutdown skipped — mode=disabled", "node", node)
	return nil
}

func (n *NoopPowerController) PowerOn(ctx context.Context, node string) error {
	slog.Info("PowerOn skipped — mode=disabled", "node", node)
	return nil
}
