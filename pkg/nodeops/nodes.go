package nodeops

import (
	"context"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ManagedNodeFilter struct {
	ManagedLabel  string
	DisabledLabel string
	IgnoreLabels  map[string]string
}

// ListManagedNodes returns all nodes with the specified managed label = "true",
// skips nodes with the disabled label = "true", and any node that matches any ignoreLabels.
func ListManagedNodes(ctx context.Context, client kubernetes.Interface, filter ManagedNodeFilter) ([]v1.Node, error) {
	allNodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var result []v1.Node
outer:
	for _, node := range allNodes.Items {
		if node.Labels[filter.ManagedLabel] != "true" {
			continue
		}
		if node.Labels[filter.DisabledLabel] == "true" {
			continue
		}
		for k, v := range filter.IgnoreLabels {
			if node.Labels[k] == v {
				continue outer
			}
		}
		result = append(result, node)
	}

	return result, nil
}

// ListShutdownNodeNames returns the names of nodes that are both managed and currently marked as powered off,
// either by annotation or in internal state tracker.
func ListShutdownNodeNames(ctx context.Context, client kubernetes.Interface, filter ManagedNodeFilter, tracker *NodeStateTracker) ([]string, error) {
	nodes, err := ListManagedNodes(ctx, client, filter)
	if err != nil {
		return nil, err
	}

	const annotationPoweredOff = "cba.dev/was-powered-off"
	var shutdown []string

	for _, node := range nodes {
		if node.Annotations[annotationPoweredOff] == "true" || tracker.IsPoweredOff(node.Name) {
			shutdown = append(shutdown, node.Name)
		}
	}

	return shutdown, nil
}
