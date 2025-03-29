package strategy

import (
	"context"
	"fmt"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
	"log/slog"
)

type ResourceAwareScaleDown struct {
	Client        *kubernetes.Clientset
	MetricsClient metricsclient.Interface
	Cfg           *config.Config
	NodeLister    func(context.Context) ([]v1.Node, error)
	PodLister     func(context.Context) ([]v1.Pod, error)
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

	nodeUsages, err := r.MetricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("fetching node metrics: %w", err)
	}

	usageMap := make(map[string]v1.ResourceList)
	for _, usage := range nodeUsages.Items {
		usageMap[usage.Name] = usage.Usage
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

		// Add real usage values
		usage := usageMap[node.Name]
		if usage != nil {
			if cpu := usage.Cpu(); cpu != nil {
				totalCPU += cpu.MilliValue()
			}
			if mem := usage.Memory(); mem != nil {
				totalMem += mem.Value()
			}
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

			slog.Debug("Pod request", "pod", pod.Name, "ns", pod.Namespace, "cpu", cpuMilli, "mem", memBytes, "node", pod.Spec.NodeName)
		}
	}

	// Add margins
	marginCPU := clusterCPU * int64(r.Cfg.ResourceBufferCPUPerc) / 100
	marginMem := clusterMem * int64(r.Cfg.ResourceBufferMemoryPerc) / 100

	// Check both request-based and usage-based thresholds independently
	canScaleRequestOK := totalCPU+marginCPU <= clusterCPU && totalMem+marginMem <= clusterMem
	canScaleUsageOK := true

	if usage, ok := usageMap[nodeName]; ok {
		usedCPU := usage.Cpu().MilliValue()
		usedMem := usage.Memory().Value()

		canScaleUsageOK = usedCPU+marginCPU <= clusterCPU && usedMem+marginMem <= clusterMem

		slog.Info("Usage check for candidate node",
			"usedCPU", usedCPU, "usedMem", usedMem,
			"canScaleUsageOK", canScaleUsageOK,
		)
	} else {
		slog.Warn("No metrics available for candidate node", "node", nodeName)
	}

	canScale := canScaleRequestOK && canScaleUsageOK

	slog.Info("Resource check",
		"canScale", canScale,
		"canScaleRequestOK", canScaleRequestOK,
		"canScaleUsageOK", canScaleUsageOK,
		"totalCPU", totalCPU, "clusterCPU", clusterCPU,
		"totalMem", totalMem, "clusterMem", clusterMem,
		"bufferCPU", marginCPU, "bufferMem", marginMem,
		"nodeCandidate", nodeName, "nodeCPU", nodeCPU, "nodeMem", nodeMem,
	)

	return canScale, nil
}
