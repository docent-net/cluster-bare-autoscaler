package strategy

import (
	"context"
	"strings"
)

type ScaleUpStrategy interface {
	ShouldScaleUp(ctx context.Context) (nodeName string, shouldScale bool, err error)
	Name() string
}

type MultiUpStrategy struct {
	Strategies []ScaleUpStrategy
}

func (m *MultiUpStrategy) ShouldScaleUp(ctx context.Context) (string, bool, error) {
	for _, s := range m.Strategies {
		node, ok, err := s.ShouldScaleUp(ctx)
		if err != nil {
			return "", false, err
		}
		if ok {
			return node, true, nil
		}
	}
	return "", false, nil
}

func (m *MultiUpStrategy) Name() string {
	names := []string{}
	for _, s := range m.Strategies {
		names = append(names, s.Name())
	}
	return "MultiUpStrategy(" + strings.Join(names, ", ") + ")"
}
