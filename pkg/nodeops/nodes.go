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

// WrapNodes transforms a list of v1.Node objects into []*NodeWrapper.
//
// Unlike the NodeWrapper itself, which encapsulates behavior and metadata for a single node,
// this helper constructs a wrapped view of all nodes in one step, injecting shared context like
// state tracker, timestamp (`now`), MAC annotation config, and ignore label rules.
//
// It is purely a utility — not tied to any method on NodeWrapper — and is intended to make
// downstream logic cleaner when operating on node collections.
func WrapNodes(nodes []v1.Node, state *NodeStateTracker, now time.Time, cfg NodeAnnotationConfig, ignore map[string]string) []*NodeWrapper {
	var result []*NodeWrapper
	for i := range nodes {
		result = append(result, NewNodeWrapper(&nodes[i], state, now, cfg, ignore))
	}
	return result
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
			slog.Debug("Skipping node due to lack or incorrect ManagedLabel", "node", node.Name)
			continue
		}
		if node.Labels[filter.DisabledLabel] == "true" {
			slog.Debug("Skipping node due to DisabledLabel set", "node", node.Name)
			continue
		}
		for k := range filter.IgnoreLabels {
			if _, exists := node.Labels[k]; exists {
				slog.Debug("Skipping node due to IgnoreLabels match (label key exists)",
					"node", node.Name,
					"label", k,
				)
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
	wrapped := WrapNodes(nodes, tracker, time.Now(), NodeAnnotationConfig{}, extraFilter.IgnoreLabels)

	for _, node := range wrapped {
		if node.IsCordoned() {
			continue
		}
		if node.IsIgnored() {
			continue
		}
		if node.IsMarkedPoweredOff() {
			continue
		}
		if node.IsReady() {
			active = append(active, *node.Node)
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
func FilterShutdownEligibleNodes(nodes []v1.Node, state *NodeStateTracker, now time.Time, cfg EligibilityConfig) []*NodeWrapper {
	var eligible []*NodeWrapper
	wrapped := WrapNodes(nodes, state, now, NodeAnnotationConfig{}, cfg.IgnoreLabels)

	for _, node := range wrapped {
		if node.IsIgnored() {
			slog.Info("Skipping node due to ignoreLabels", "node", node.Name)
			continue
		}
		if node.IsMarkedPoweredOff() {
			slog.Info("Skipping node marked as powered off", "node", node.Name)
			continue
		}
		if node.IsCordoned() {
			slog.Info("Skipping node because it is cordoned", "node", node.Name)
			continue
		}
		if node.IsInShutdownCooldown(cfg.Cooldown) {
			slog.Info("Skipping node due to shutdown cooldown", "node", node.Name)
			continue
		}
		if node.IsInBootCooldown(cfg.BootCooldown) {
			slog.Info("Skipping node due to boot cooldown", "node", node.Name)
			continue
		}
		eligible = append(eligible, node)
	}

	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(eligible), func(i, j int) {
		eligible[i], eligible[j] = eligible[j], eligible[i]
	})

	return eligible
}
