package strategy

import (
	"context"
	"fmt"
	"log/slog"
)

// ScaleDownStrategy evaluates if a node should be scaled down.
type ScaleDownStrategy interface {
	ShouldScaleDown(ctx context.Context, nodeName string) (bool, error)
}

type MultiStrategy struct {
	Strategies []ScaleDownStrategy
}

func (m *MultiStrategy) ShouldScaleDown(ctx context.Context, nodeName string) (bool, error) {
	for _, strategy := range m.Strategies {
		ok, err := strategy.ShouldScaleDown(ctx, nodeName)
		if err != nil {
			return false, fmt.Errorf("strategy %T failed: %w", strategy, err)
		}
		if !ok {
			slog.Info("Scale-down denied by strategy", "node", nodeName, "strategy", fmt.Sprintf("%T", strategy))
			return false, nil
		}
	}
	return true, nil
}
