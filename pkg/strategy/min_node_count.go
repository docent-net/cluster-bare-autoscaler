package strategy

import (
	"context"
	"log/slog"

	v1 "k8s.io/api/core/v1"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
)

type MinNodeCountScaleUp struct {
	Cfg          *config.Config
	ActiveNodes  func(ctx context.Context) ([]v1.Node, error)
	ShutdownList func(ctx context.Context) []string
}

func (s *MinNodeCountScaleUp) Name() string {
	return "MinNodeCount"
}

func (s *MinNodeCountScaleUp) ShouldScaleUp(ctx context.Context) (string, bool, error) {
	active, err := s.ActiveNodes(ctx)
	if err != nil {
		return "", false, err
	}

	if len(active) >= s.Cfg.MinNodes {
		slog.Debug("MinNodeCountScaleUp: current nodes meet or exceed minNodes", "current", len(active), "minNodes", s.Cfg.MinNodes)
		return "", false, nil
	}

	shutdown := s.ShutdownList(ctx)
	if len(shutdown) == 0 {
		slog.Debug("MinNodeCountScaleUp: below minNodes but no available shutdown nodes to power on",
			"activeNodes", len(active),
			"shutdownCandidates", len(shutdown),
			"minNodes", s.Cfg.MinNodes)

		return "", false, nil
	}

	slog.Info("MinNodeCountScaleUp: triggering scale-up",
		"reason", "below minNodes",
		"candidate", shutdown[0],
		"activeNodes", len(active),
		"shutdownCandidates", len(shutdown),
		"minNodes", s.Cfg.MinNodes)

	return shutdown[0], true, nil
}
