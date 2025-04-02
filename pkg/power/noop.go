package power

import (
	"context"
	"log/slog"
)

type NoopPowerOnController struct{}

func (n *NoopPowerOnController) PowerOn(ctx context.Context, node string) error {
	slog.Info("PowerOn skipped — mode=disabled", "node", node)
	return nil
}

type NoopShutdownController struct{}

func (n *NoopShutdownController) Shutdown(ctx context.Context, node string) error {
	slog.Info("Shutdown skipped — mode=disabled", "node", node)
	return nil
}
