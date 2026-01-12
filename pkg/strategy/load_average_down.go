package strategy

import (
	"context"
	"log/slog"
	"maps"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"k8s.io/client-go/kubernetes"
)

type LoadAverageScaleDown struct {
	Client                    kubernetes.Interface
	Cfg                       *config.Config
	PodLabel                  string
	Namespace                 string
	HTTPPort                  int
	HTTPTimeout               time.Duration
	NodeThreshold             float64
	ClusterWideThreshold      float64
	DryRunNodeLoadOverride    *float64
	DryRunClusterLoadOverride *float64
	ClusterEvalMode           ClusterLoadEvalMode
	IgnoreLabels              map[string]string
}

func (l *LoadAverageScaleDown) Name() string {
	return "LoadAverage"
}

func (l *LoadAverageScaleDown) ShouldScaleDown(ctx context.Context, nodeName string) (bool, error) {
	normalized, err := l.getNormalizedLoadForNode(ctx, nodeName)
	if err != nil {
		return false, err
	}

	if normalized >= l.NodeThreshold {
		slog.Info("Node load too high for scale-down", "node", nodeName, "load", normalized, "threshold", l.NodeThreshold)
		return false, nil
	}

	aggregate, err := l.getClusterAggregateLoad(ctx, nodeName)
	if err != nil {
		return false, nil
	}

	slog.Info("Cluster-wide load evaluation",
		"aggregateLoad", aggregate,
		"clusterWideThreshold", l.ClusterWideThreshold,
		"evalMode", l.ClusterEvalMode,
	)

	if aggregate >= l.ClusterWideThreshold {
		slog.Info("Cluster-wide load too high to scale down node", "aggregateLoad", aggregate, "threshold", l.ClusterWideThreshold)
		return false, nil
	}

	return true, nil
}

func (l *LoadAverageScaleDown) getNormalizedLoadForNode(ctx context.Context, nodeName string) (float64, error) {
	if l.DryRunNodeLoadOverride != nil {
		slog.Info("Dry-run override: using normalized load value", "node", nodeName, "value", *l.DryRunNodeLoadOverride)
		return *l.DryRunNodeLoadOverride, nil
	}
	return NewClusterLoadUtils(l.Client, l.Namespace, l.PodLabel, l.HTTPPort, l.HTTPTimeout).FetchNormalizedLoad(ctx, nodeName)
}

func (l *LoadAverageScaleDown) getClusterAggregateLoad(ctx context.Context, excludeNode string) (float64, error) {
	utils := NewClusterLoadUtils(l.Client, l.Namespace, l.PodLabel, l.HTTPPort, l.HTTPTimeout)

	exclude := map[string]string{}
	if l.Cfg.NodeLabels.Disabled != "" {
		exclude[l.Cfg.NodeLabels.Disabled] = "true"
	}
	maps.Copy(exclude, l.Cfg.LoadAverageStrategy.ExcludeFromAggregateLabels)

	return utils.GetClusterAggregateLoad(ctx, exclude, excludeNode, l.DryRunClusterLoadOverride, l.ClusterEvalMode)
}
