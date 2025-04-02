package strategy

import (
	"context"
	"log/slog"
	"time"

	"k8s.io/client-go/kubernetes"
)

type LoadAverageScaleUp struct {
	Client               kubernetes.Interface
	Namespace            string
	PodLabel             string
	HTTPPort             int
	HTTPTimeout          time.Duration
	ClusterEvalMode      ClusterLoadEvalMode
	ClusterWideThreshold float64
	DryRunOverride       *float64
	IgnoreLabels         map[string]string

	ShutdownCandidates func(ctx context.Context) []string
}

func (s *LoadAverageScaleUp) Name() string {
	return "LoadAverageScaleUp"
}

func (s *LoadAverageScaleUp) ShouldScaleUp(ctx context.Context) (string, bool, error) {
	candidates := s.ShutdownCandidates(ctx)
	if len(candidates) == 0 {
		slog.Debug("LoadAverageScaleUp: no shutdown candidates available")
		return "", false, nil
	}

	var aggregate float64
	if s.DryRunOverride != nil {
		aggregate = *s.DryRunOverride
		slog.Info("Dry-run override: using cluster-wide load", "value", aggregate)
	} else {
		utils := NewClusterLoadUtils(s.Client, s.Namespace, s.PodLabel, s.HTTPPort, s.HTTPTimeout)
		var err error
		aggregate, err = utils.GetClusterAggregateLoad(ctx, s.IgnoreLabels, "", s.DryRunOverride, s.ClusterEvalMode)
		if err != nil {
			return "", false, nil
		}
	}

	slog.Info("Cluster-wide load evaluation",
		"aggregateLoad", aggregate,
		"clusterWideThreshold", s.ClusterWideThreshold,
		"evalMode", s.ClusterEvalMode,
	)

	if aggregate < s.ClusterWideThreshold {
		return "", false, nil
	}

	return candidates[0], true, nil
}
