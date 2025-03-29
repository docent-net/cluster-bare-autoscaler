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
			if cpu := node.Status.Allocatable.Cpu(); cpu != nil {
				nodeCPU = cpu.MilliValue()
			}
			if mem := node.Status.Allocatable.Memory(); mem != nil {
				nodeMem = mem.Value()
			}
			continue
		}
		if cpu := node.Status.Allocatable.Cpu(); cpu != nil {
			clusterCPU += cpu.MilliValue()
		}
		if mem := node.Status.Allocatable.Memory(); mem != nil {
			clusterMem += mem.Value()
		}
	}

	for _, pod := range pods {
		for _, c := range pod.Spec.Containers {
			var cpuMilli, memBytes int64

			if cpu := c.Resources.Requests.Cpu(); cpu != nil {
				cpuMilli = cpu.MilliValue()
				totalCPU += cpuMilli
			}
			if mem := c.Resources.Requests.Memory(); mem != nil {
				memBytes = mem.Value()
				totalMem += memBytes
			}

			slog.Info("Pod request", "pod", pod.Name, "ns", pod.Namespace, "cpu", cpuMilli, "mem", memBytes, "node", pod.Spec.NodeName)
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

	return canScale, nil
}
