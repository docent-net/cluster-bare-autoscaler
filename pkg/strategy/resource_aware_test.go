package strategy

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
)

func TestResourceAwareScaleDown_BlocksOnCPUOnly(t *testing.T) {
	strat := &ResourceAwareScaleDown{
		Cfg: &config.Config{
			ResourceBufferCPUPerc:    10,
			ResourceBufferMemoryPerc: 10,
		},
		NodeLister: func(ctx context.Context) ([]v1.Node, error) {
			return []v1.Node{
				newNode("node1", "2000m", "8Gi"),
				newNode("node2", "2000m", "8Gi"),
			}, nil
		},
		PodLister: func(ctx context.Context) ([]v1.Pod, error) {
			return []v1.Pod{
				newPod("pod1", "1900m", "2Gi", "node1"),
			}, nil
		},
	}

	ok, err := strat.ShouldScaleDown(context.Background(), "node2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-down to be blocked due to CPU, but it was allowed")
	}
}

func TestResourceAwareScaleDown_BlocksOnMemoryOnly(t *testing.T) {
	strat := &ResourceAwareScaleDown{
		Cfg: &config.Config{
			ResourceBufferCPUPerc:    10,
			ResourceBufferMemoryPerc: 10,
		},
		NodeLister: func(ctx context.Context) ([]v1.Node, error) {
			return []v1.Node{
				newNode("node1", "8", "2Gi"),
				newNode("node2", "8", "2Gi"),
			}, nil
		},
		PodLister: func(ctx context.Context) ([]v1.Pod, error) {
			return []v1.Pod{
				newPod("pod1", "500m", "1.9Gi", "node1"),
			}, nil
		},
	}

	ok, err := strat.ShouldScaleDown(context.Background(), "node2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-down to be blocked due to Memory, but it was allowed")
	}
}

func TestResourceAwareScaleDown_AllowsAtExactLimit(t *testing.T) {
	strat := &ResourceAwareScaleDown{
		Cfg: &config.Config{
			ResourceBufferCPUPerc:    0,
			ResourceBufferMemoryPerc: 0,
		},
		NodeLister: func(ctx context.Context) ([]v1.Node, error) {
			return []v1.Node{
				newNode("node1", "2000m", "2Gi"),
				newNode("node2", "2000m", "2Gi"),
			}, nil
		},
		PodLister: func(ctx context.Context) ([]v1.Pod, error) {
			return []v1.Pod{
				newPod("pod1", "2000m", "2Gi", "node1"),
			}, nil
		},
	}

	ok, err := strat.ShouldScaleDown(context.Background(), "node2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected scale-down to be allowed at exact limit, but it was blocked")
	}
}

func TestResourceAwareScaleDown_AllowsShutdownWhenPlentyOfBuffer(t *testing.T) {
	strat := &ResourceAwareScaleDown{
		Cfg: &config.Config{
			ResourceBufferCPUPerc:    10,
			ResourceBufferMemoryPerc: 10,
		},
		NodeLister: func(ctx context.Context) ([]v1.Node, error) {
			return []v1.Node{
				newNode("node1", "4000m", "8Gi"),
				newNode("node2", "4000m", "8Gi"), // candidate
			}, nil
		},
		PodLister: func(ctx context.Context) ([]v1.Pod, error) {
			return []v1.Pod{
				newPod("pod1", "1000m", "1Gi", "node1"),
				newPod("pod2", "1000m", "1Gi", "node1"),
			}, nil
		},
	}

	ok, err := strat.ShouldScaleDown(context.Background(), "node2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected to allow scale-down, but didn't")
	}
}

func TestResourceAwareScaleDown_BlocksShutdownIfTooTight(t *testing.T) {
	strat := &ResourceAwareScaleDown{
		Cfg: &config.Config{
			ResourceBufferCPUPerc:    10,
			ResourceBufferMemoryPerc: 10,
		},
		NodeLister: func(ctx context.Context) ([]v1.Node, error) {
			return []v1.Node{
				newNode("node1", "2000m", "2Gi"),
				newNode("node2", "2000m", "2Gi"), // candidate
			}, nil
		},
		PodLister: func(ctx context.Context) ([]v1.Pod, error) {
			return []v1.Pod{
				newPod("pod1", "1500m", "1.5Gi", "node1"),
				newPod("pod2", "500m", "500Mi", "node1"),
			}, nil
		},
	}

	ok, err := strat.ShouldScaleDown(context.Background(), "node2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected scale-down to be blocked, but it was allowed")
	}
}

func newNode(name string, cpu string, mem string) v1.Node {
	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse(cpu),
				v1.ResourceMemory: resource.MustParse(mem),
			},
		},
	}
}

func newPod(name string, cpu string, mem string, nodeName string) v1.Pod {
	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1.PodSpec{
			NodeName: nodeName,
			Containers: []v1.Container{
				{
					Name: name + "-container",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse(cpu),
							v1.ResourceMemory: resource.MustParse(mem),
						},
					},
				},
			},
		},
	}
}
