package strategy

import (
	"context"
	"fmt"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"log/slog"
)

type ResourceAwareScaleDown struct {
	Client     *kubernetes.Clientset
	Cfg        *config.Config
	NodeLister func(context.Context) ([]v1.Node, error)
	PodLister  func(context.Context) ([]v1.Pod, error)
}

func (r *ResourceAwareScaleDown) ShouldScaleDown(ctx context.Context, nodeName string) (bool, error) {
	nodes, err := r.NodeLister(ctx)
	if err != nil {
		return false, fmt.Errorf("listing nodes: %w", err)
	}

	pods, err := r.PodLister(ctx)
	if err != nil {
		return false, fmt.Errorf("listing pods: %w", err)
	}

	var totalCPU, totalMem int64
	var clusterCPU, clusterMem int64
	var nodeCPU, nodeMem int64

	for _, node := range nodes {
		if node.Name == nodeName {
			nodeCPU, _ = node.Status.Allocatable.Cpu().AsInt64()
			nodeMem, _ = node.Status.Allocatable.Memory().AsInt64()
			continue // skip the candidate node
		}
		clusterCPU += node.Status.Allocatable.Cpu().ScaledValue(0)
		clusterMem += node.Status.Allocatable.Memory().ScaledValue(0)
	}

	for _, pod := range pods {
		if pod.Spec.NodeName == "" || pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
			continue
		}
		for _, c := range pod.Spec.Containers {
			reqCPU := c.Resources.Requests.Cpu().ScaledValue(0)
			reqMem := c.Resources.Requests.Memory().ScaledValue(0)
			totalCPU += reqCPU
			totalMem += reqMem
		}
	}

	// Add margin
	marginCPU := clusterCPU * int64(r.Cfg.ResourceBufferPercentage) / 100
	marginMem := clusterMem * int64(r.Cfg.ResourceBufferPercentage) / 100

	canScale := totalCPU+marginCPU <= clusterCPU && totalMem+marginMem <= clusterMem
	slog.Info("Resource check",
		"canScale", canScale,
		"totalCPU", totalCPU, "clusterCPU", clusterCPU,
		"totalMem", totalMem, "clusterMem", clusterMem,
		"bufferCPU", marginCPU, "bufferMem", marginMem,
		"nodeCandidate", nodeName, "nodeCPU", nodeCPU, "nodeMem", nodeMem,
	)

	if !canScale {
		return false, nil
	}
	return true, nil
}
