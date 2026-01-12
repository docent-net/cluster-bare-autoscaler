package nodeops

import (
	"context"
	"fmt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/config"
	"github.com/docent-net/cluster-bare-autoscaler/pkg/power"
	"golang.org/x/exp/slog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// UncordonNode sets the Unschedulable field of a node to false.
func UncordonNode(ctx context.Context, client kubernetes.Interface, nodeName string) error {
	return retry.OnError(retry.DefaultBackoff, apierrors.IsConflict, func() error {
		node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("fetch node: %w", err)
		}

		if !node.Spec.Unschedulable {
			return nil
		}

		nodeCopy := node.DeepCopy()
		nodeCopy.Spec.Unschedulable = false

		_, err = client.CoreV1().Nodes().Update(ctx, nodeCopy, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("uncordon update: %w", err)
		}

		return nil
	})
}

// ClearPoweredOffAnnotation removes the powered-off annotation from the node.
func ClearPoweredOffAnnotation(ctx context.Context, client kubernetes.Interface, nodeName string) error {
	patch := fmt.Appendf(nil, `{"metadata":{"annotations":{"%s":null}}}`, AnnotationPoweredOff)
	_, err := client.CoreV1().Nodes().Patch(ctx, nodeName, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("remove annotation: %w", err)
	}
	return nil
}

// PowerOnAndMarkBooted performs power-on logic and updates state and annotations.
func PowerOnAndMarkBooted(ctx context.Context, node *NodeWrapper, cfg *config.Config, client kubernetes.Interface, powerOner power.PowerOnController, state *NodeStateTracker, dryRun bool) error {
	slog.Info("Powering on node", "node", node.Name)

	if dryRun {
		slog.Info("Dry-run: would power on", "node", node.Name)
		return nil
	}

	mac := GetMACAddressFromNode(*node.Node, NodeAnnotationConfig{
		MAC: cfg.NodeAnnotations.MAC,
	})
	if mac == "" {
		return fmt.Errorf("missing MAC address for node %q", node.Name)
	}

	if err := powerOner.PowerOn(ctx, node.Name, mac); err != nil {
		return fmt.Errorf("power on: %w", err)
	}

	if err := UncordonNode(ctx, client, node.Name); err != nil {
		slog.Warn("Failed to uncordon node", "node", node.Name, "err", err)
		return err
	}

	if err := ClearPoweredOffAnnotation(ctx, client, node.Name); err != nil {
		slog.Warn("Failed to clear powered-off annotation", "node", node.Name, "err", err)
		return err
	}

	state.MarkGlobalShutdown()
	state.MarkBooted(node.Name)

	return nil
}

func ForcePowerOnAllNodes(
	ctx context.Context,
	client kubernetes.Interface,
	cfg *config.Config,
	state *NodeStateTracker,
	powerOner power.PowerOnController,
	dryRun bool,
) error {
	slog.Warn("ForcePowerOnAllNodes is active — overriding strategy logic and powering on all managed nodes")

	nodes, err := ListManagedNodes(ctx, client, ManagedNodeFilter{
		ManagedLabel:  cfg.NodeLabels.Managed,
		DisabledLabel: cfg.NodeLabels.Disabled,
		IgnoreLabels:  cfg.IgnoreLabels,
	})
	if err != nil {
		return fmt.Errorf("listing managed nodes: %w", err)
	}

	now := time.Now()
	for _, node := range nodes {
		if IsNodeReady(&node) {
			slog.Info("Skipping node already marked Ready", "node", node.Name)
			continue
		}

		wrapped := NewNodeWrapper(&node, state, now, NodeAnnotationConfig{
			MAC: cfg.NodeAnnotations.MAC,
		}, cfg.IgnoreLabels)

		slog.Info("Force powering on", "node", node.Name)
		if err := PowerOnAndMarkBooted(ctx, wrapped, cfg, client, powerOner, state, dryRun); err != nil {
			slog.Warn("Failed to force power on node", "node", node.Name, "err", err)
			continue
		}
	}

	return nil
}
