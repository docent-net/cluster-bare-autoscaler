package nodeops

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// FindPodIPOnNode locates a Pod by label running on the specified node and returns its IP.
func FindPodIPOnNode(ctx context.Context, client kubernetes.Interface, namespace, label, nodeName string) (string, error) {
	selector, err := labels.Parse(label)
	if err != nil {
		return "", err
	}
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return "", fmt.Errorf("listing pods: %w", err)
	}

	for _, pod := range pods.Items {
		if pod.Spec.NodeName == nodeName && pod.Status.PodIP != "" {
			return pod.Status.PodIP, nil
		}
	}

	return "", fmt.Errorf("no pod with label %q found on node %s", label, nodeName)
}
