package strategy

import (
	"context"
	"fmt"
	"k8s.io/client-go/kubernetes"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/metrics/pkg/client/clientset/versioned"
	"log/slog"
)

type ResourceAwareScaleDown struct {
	Client        *kubernetes.Clientset
	Cfg           *config.Config
	NodeLister    func(context.Context) ([]v1.Node, error)
	PodLister     func(context.Context) ([]v1.Pod, error)
	MetricsClient versioned.Interface
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

	totalCPURequest, totalMemRequest := r.SumRequests(pods)
	totalCPUUsage, totalMemUsage, clusterCPU, clusterMem, nodeCPU, nodeMem, usedCPU, usedMem := r.AnalyzeNodes(nodes, usageMap, nodeName)

	marginCPU := clusterCPU * int64(r.Cfg.ResourceBufferCPUPerc) / 100
	marginMem := clusterMem * int64(r.Cfg.ResourceBufferMemoryPerc) / 100

	canScaleRequestOK := totalCPURequest+marginCPU <= clusterCPU && totalMemRequest+marginMem <= clusterMem
	canScaleUsageOK := usedCPU+marginCPU <= clusterCPU && usedMem+marginMem <= clusterMem

	slog.Info("Request-based scale-down check",
		"canScaleRequestOK", canScaleRequestOK,
		"totalCPURequest", totalCPURequest,
		"totalMemRequest", totalMemRequest,
		"clusterCPU", clusterCPU,
		"clusterMem", clusterMem,
		"bufferCPU", marginCPU,
		"bufferMem", marginMem,
		"nodeCandidate", nodeName,
		"nodeCPU", nodeCPU,
		"nodeMem", nodeMem,
	)

	slog.Info("Usage-based scale-down check",
		"canScaleUsageOK", canScaleUsageOK,
		"totalCPUUsage", totalCPUUsage,
		"totalMemUsage", totalMemUsage,
		"usedCPU", usedCPU,
		"usedMem", usedMem,
		"nodeCandidate", nodeName,
	)

	return canScaleRequestOK && canScaleUsageOK, nil
}

func (r *ResourceAwareScaleDown) Name() string {
	return "ResourceAware"
}

func (r *ResourceAwareScaleDown) SumRequests(pods []v1.Pod) (int64, int64) {
	var totalCPURequest, totalMemRequest int64
	for _, pod := range pods {
		for _, c := range pod.Spec.Containers {
			if cpu := c.Resources.Requests.Cpu(); cpu != nil {
				totalCPURequest += cpu.MilliValue()
			}
			if mem := c.Resources.Requests.Memory(); mem != nil {
				totalMemRequest += mem.Value()
			}
			slog.Debug("Pod request", "pod", pod.Name, "ns", pod.Namespace)
		}
	}
	return totalCPURequest, totalMemRequest
}

func (r *ResourceAwareScaleDown) AnalyzeNodes(
	nodes []v1.Node,
	usageMap map[string]v1.ResourceList,
	nodeName string,
) (int64, int64, int64, int64, int64, int64, int64, int64) {
	var totalCPUUsage, totalMemUsage, clusterCPU, clusterMem int64
	var nodeCPU, nodeMem, usedCPU, usedMem int64

	for _, node := range nodes {
		if node.Name == nodeName {
			if cpu := node.Status.Allocatable.Cpu(); cpu != nil {
				nodeCPU = cpu.MilliValue()
			}
			if mem := node.Status.Allocatable.Memory(); mem != nil {
				nodeMem = mem.Value()
			}
			if usage, ok := usageMap[nodeName]; ok {
				if cpu := usage.Cpu(); cpu != nil {
					usedCPU = cpu.MilliValue()
				}
				if mem := usage.Memory(); mem != nil {
					usedMem = mem.Value()
				}
			} else {
				slog.Warn("No metrics available for candidate node", "node", nodeName)
			}
			continue
		}

		if cpu := node.Status.Allocatable.Cpu(); cpu != nil {
			clusterCPU += cpu.MilliValue()
		}
		if mem := node.Status.Allocatable.Memory(); mem != nil {
			clusterMem += mem.Value()
		}

		if usage := usageMap[node.Name]; usage != nil {
			if cpu := usage.Cpu(); cpu != nil {
				totalCPUUsage += cpu.MilliValue()
			}
			if mem := usage.Memory(); mem != nil {
				totalMemUsage += mem.Value()
			}
		}
	}

	return totalCPUUsage, totalMemUsage, clusterCPU, clusterMem, nodeCPU, nodeMem, usedCPU, usedMem
}
