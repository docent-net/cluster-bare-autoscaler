package nodeops

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ManagedNodeFilter struct {
	ManagedLabel  string
	DisabledLabel string
	IgnoreLabels  map[string]string
}

const AnnotationPoweredOff = "cba.dev/was-powered-off"

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

	var shutdown []string

	for _, node := range nodes {
		if node.Annotations[AnnotationPoweredOff] == "true" || tracker.IsPoweredOff(node.Name) {
			shutdown = append(shutdown, node.Name)
		}
	}

	return shutdown, nil
}

type ActiveNodeFilter struct {
	IgnoreLabels map[string]string
}

// ListActiveNodes returns managed, schedulable, Ready nodes excluding ignored and powered-off ones.
func ListActiveNodes(ctx context.Context, client kubernetes.Interface, tracker *NodeStateTracker, filter ManagedNodeFilter, extraFilter ActiveNodeFilter) ([]v1.Node, error) {
	nodes, err := ListManagedNodes(ctx, client, filter)
	if err != nil {
		return nil, err
	}

	var active []v1.Node
outer:
	for _, node := range nodes {
		// Skip unschedulable
		if node.Spec.Unschedulable {
			continue
		}

		// Skip ignored labels
		for k, v := range extraFilter.IgnoreLabels {
			if val, ok := node.Labels[k]; ok && val == v {
				continue outer
			}
		}

		// Skip annotation-based powered off
		if val, ok := node.Annotations[AnnotationPoweredOff]; ok && val == "true" {
			continue
		}

		// Skip state-tracked powered off
		if tracker.IsPoweredOff(node.Name) {
			continue
		}

		// Must be Ready
		for _, cond := range node.Status.Conditions {
			if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
				active = append(active, node)
				break
			}
		}
	}

	return active, nil
}

type EligibilityConfig struct {
	Cooldown     time.Duration
	BootCooldown time.Duration
	IgnoreLabels map[string]string
}

// FilterEligibleNodes returns nodes that pass filtering criteria:
// - not ignored by label
// - not marked powered-off
// - not cordoned
// - not in cooldown
func FilterShutdownEligibleNodes(nodes []v1.Node, state *NodeStateTracker, now time.Time, cfg EligibilityConfig) []v1.Node {
	var eligible []v1.Node

outer:
	for _, node := range nodes {
		for key, val := range cfg.IgnoreLabels {
			if nodeVal, ok := node.Labels[key]; ok && nodeVal == val {
				slog.Info("Skipping node due to ignoreLabels", "node", node.Name, "label", key)
				continue outer
			}
		}

		if val, ok := node.Annotations[AnnotationPoweredOff]; ok && val == "true" {
			slog.Info("Skipping node marked as powered-off (annotation)", "node", node.Name)
			continue
		}

		if node.Spec.Unschedulable {
			slog.Info("Skipping node because it is already cordoned", "node", node.Name)
			continue
		}

		if state.IsInCooldown(node.Name, now, cfg.Cooldown) {
			slog.Info("Skipping node due to shutdown cooldown", "node", node.Name)
			continue
		}

		if state.IsBootCooldownActive(node.Name, now, cfg.BootCooldown) {
			slog.Info("Skipping node due to boot cooldown", "node", node.Name)
			continue
		}

		if state.IsPoweredOff(node.Name) {
			slog.Info("Skipping node: already powered off", "node", node.Name)
			continue
		}

		eligible = append(eligible, node)
	}

	// Shuffle to avoid always picking the same node
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(eligible), func(i, j int) {
		eligible[i], eligible[j] = eligible[j], eligible[i]
	})

	return eligible
}
