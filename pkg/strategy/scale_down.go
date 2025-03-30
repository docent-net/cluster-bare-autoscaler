package strategy

import (
	"context"
	"log/slog"
	"strings"
)

// ScaleDownStrategy evaluates if a node should be scaled down.
type ScaleDownStrategy interface {
	ShouldScaleDown(ctx context.Context, nodeName string) (bool, error)
	Name() string
}

type MultiStrategy struct {
	Strategies []ScaleDownStrategy
}

func (m *MultiStrategy) Name() string {
	var parts []string
	for _, s := range m.Strategies {
		parts = append(parts, s.Name())
	}
	return "MultiStrategy(" + strings.Join(parts, ", ") + ")"
}

func (m *MultiStrategy) ShouldScaleDown(ctx context.Context, nodeName string) (bool, error) {
	for _, s := range m.Strategies {
		ok, err := s.ShouldScaleDown(ctx, nodeName)
		if err != nil {
			slog.Warn("Strategy returned error", "strategy", s.Name(), "err", err)
			return false, err
		}
		if !ok {
			slog.Info("Strategy denied scale-down", "strategy", s.Name(), "node", nodeName)
			return false, nil
		}
		slog.Debug("Strategy approved scale-down", "strategy", s.Name(), "node", nodeName)
	}
	return true, nil
}
