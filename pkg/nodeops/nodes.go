package nodeops

import (
	"context"
	"fmt"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"log/slog"
	"math/rand"
	"sort"
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
// either by annotation or in internal state tracker. Nodes are sorted by the oldest powered-off first.
func ListShutdownNodeNames(ctx context.Context, client kubernetes.Interface, filter ManagedNodeFilter, tracker *NodeStateTracker) ([]string, error) {
	nodes, err := ListManagedNodes(ctx, client, filter)
	if err != nil {
		return nil, err
	}

	type item struct {
		name  string
		since time.Time
	}
	var list []item

	for _, node := range nodes {
		if t, ok := PoweredOffSince(node); ok {
			list = append(list, item{name: node.Name, since: t})
			continue
		}
		if tracker.IsPoweredOff(node.Name) {
			// No annotation timestamp (legacy/in-memory) → treat as very old to rotate first
			list = append(list, item{name: node.Name, since: time.Unix(0, 0).UTC()})
		}
	}

	// Oldest powered-off first
	sort.Slice(list, func(i, j int) bool {
		return list[i].since.Before(list[j].since)
	})

	out := make([]string, len(list))
	for i := range list {
		out[i] = list[i].name
	}
	return out, nil
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

	rand.Shuffle(len(eligible), func(i, j int) {
		eligible[i], eligible[j] = eligible[j], eligible[i]
	})

	return eligible
}

func ShouldIgnoreNodeDueToLabels(node v1.Node, labels map[string]string) bool {
	for k, v := range labels {
		if val, ok := node.Labels[k]; ok && val == v {
			return true
		}
	}
	return false
}

func RecoverUnexpectedlyBootedNodes(ctx context.Context, client kubernetes.Interface, cfg *config.Config, dryRun bool) error {
	nodes, err := ListManagedNodes(ctx, client, ManagedNodeFilter{
		ManagedLabel:  cfg.NodeLabels.Managed,
		DisabledLabel: cfg.NodeLabels.Disabled,
		IgnoreLabels:  cfg.IgnoreLabels,
	})
	if err != nil {
		return fmt.Errorf("failed to list nodes for recovery: %w", err)
	}

	for _, node := range nodes {
		if !IsNodeReady(&node) {
			slog.Debug("Skipping node because it is not Ready", "node", node.Name)
			continue
		}
		if _, hasAnnotation := node.Annotations[AnnotationPoweredOff]; !hasAnnotation {
			continue
		}
		if ShouldIgnoreNodeDueToLabels(node, cfg.IgnoreLabels) {
			continue
		}
		if !node.Spec.Unschedulable {
			slog.Debug("Skipping node that is not cordoned", "node", node.Name)
			continue
		}

		slog.Info("Recovering unexpectedly booted node", "node", node.Name)

		if dryRun {
			slog.Debug("Dry-run: would uncordon and clear annotation", "node", node.Name)
			continue
		}

		// Step 1: Uncordon
		err := retry.OnError(retry.DefaultBackoff, apierrors.IsConflict, func() error {
			nodeLatest, err := client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("refetch node: %w", err)
			}

			nodeCopy := nodeLatest.DeepCopy()
			nodeCopy.Spec.Unschedulable = false

			_, err = client.CoreV1().Nodes().Update(ctx, nodeCopy, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("update node: %w", err)
			}
			return nil
		})
		if err != nil {
			slog.Warn("Failed to uncordon node after retries", "node", node.Name, "err", err)
			continue
		}

		// Step 2: Remove powered-off annotation
		patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, AnnotationPoweredOff))
		_, err = client.CoreV1().Nodes().Patch(ctx, node.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			slog.Warn("Failed to clear powered-off annotation", "node", node.Name, "err", err)
			continue
		}

		slog.Info("Recovered node successfully", "node", node.Name)
	}

	return nil
}

// IsNodeReady returns true if the node has a Ready condition with status True.
func IsNodeReady(node *v1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}
