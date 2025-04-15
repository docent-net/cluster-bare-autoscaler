package nodeops

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"log/slog"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type NodeWrapper struct {
	*v1.Node
	State      *NodeStateTracker
	Now        time.Time
	MACKeys    NodeAnnotationConfig
	IgnoreKeys map[string]string
}

func NewNodeWrapper(n *v1.Node, state *NodeStateTracker, now time.Time, keys NodeAnnotationConfig, ignore map[string]string) *NodeWrapper {
	return &NodeWrapper{
		Node:       n,
		State:      state,
		Now:        now,
		MACKeys:    keys,
		IgnoreKeys: ignore,
	}
}

func (n *NodeWrapper) SetDiscoveredMAC(ctx context.Context, client kubernetes.Interface, mac string, dryRun bool) error {
	if dryRun {
		slog.Debug("Dry-run: would annotate node with discovered MAC", "node", n.Name, "mac", mac)
		return nil
	}

	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, AnnotationMACAuto, mac))
	_, err := client.CoreV1().Nodes().Patch(ctx, n.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		slog.Warn("Failed to patch node with discovered MAC", "node", n.Name, "err", err)
	}
	return err
}

func (n *NodeWrapper) IsInShutdownCooldown(duration time.Duration) bool {
	return n.State != nil && n.State.IsInCooldown(n.Name, n.Now, duration)
}

func (n *NodeWrapper) IsInBootCooldown(duration time.Duration) bool {
	return n.State != nil && n.State.IsBootCooldownActive(n.Name, n.Now, duration)
}

func (n *NodeWrapper) HasDiscoveredMACAddr() bool {
	return n.Annotations[AnnotationMACAuto] != ""
}

func (n *NodeWrapper) HasManualMACOverride() bool {
	return n.Annotations[AnnotationMACManual] != ""
}

func (n *NodeWrapper) IsCordoned() bool {
	return n.Spec.Unschedulable
}

func (n *NodeWrapper) IsReady() bool {
	for _, cond := range n.Status.Conditions {
		if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

func (n *NodeWrapper) IsMarkedPoweredOff() bool {
	if val, ok := n.Annotations[AnnotationPoweredOff]; ok && val == "true" {
		return true
	}
	return n.State != nil && n.State.IsPoweredOff(n.Name)
}

func (n *NodeWrapper) IsIgnored() bool {
	for key, val := range n.IgnoreKeys {
		if nodeVal, ok := n.Labels[key]; ok && nodeVal == val {
			return true
		}
	}
	return false
}

func (n *NodeWrapper) GetEffectiveMACAddress() string {
	manual := n.Annotations[AnnotationMACManual]
	if manual != "" {
		return manual
	}
	key := n.MACKeys.MAC
	if key == "" {
		key = DefaultMACAnnotation
	}
	return n.Annotations[key]
}
