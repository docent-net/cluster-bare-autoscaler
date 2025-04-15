package nodeops_test

import (
	"context"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
)

func TestNodeWrapper_IsCordoned(t *testing.T) {
	n := &v1.Node{Spec: v1.NodeSpec{Unschedulable: true}}
	wrapper := nodeops.NewNodeWrapper(n, nil, time.Now(), nodeops.NodeAnnotationConfig{}, nil)
	if !wrapper.IsCordoned() {
		t.Errorf("expected IsCordoned to be true")
	}
}

func TestNodeWrapper_IsReady(t *testing.T) {
	n := &v1.Node{
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{Type: v1.NodeReady, Status: v1.ConditionTrue},
			},
		},
	}
	wrapper := nodeops.NewNodeWrapper(n, nil, time.Now(), nodeops.NodeAnnotationConfig{}, nil)
	if !wrapper.IsReady() {
		t.Errorf("expected IsReady to be true")
	}
}

func TestNodeWrapper_IsMarkedPoweredOff(t *testing.T) {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node1",
			Annotations: map[string]string{nodeops.AnnotationPoweredOff: "true"},
		},
	}
	wrapper := nodeops.NewNodeWrapper(n, nodeops.NewNodeStateTracker(), time.Now(), nodeops.NodeAnnotationConfig{}, nil)
	if !wrapper.IsMarkedPoweredOff() {
		t.Errorf("expected IsMarkedPoweredOff to be true")
	}
}

func TestNodeWrapper_IsIgnored(t *testing.T) {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"skip-me": "yes"},
		},
	}
	wrapper := nodeops.NewNodeWrapper(n, nil, time.Now(), nodeops.NodeAnnotationConfig{}, map[string]string{"skip-me": "yes"})
	if !wrapper.IsIgnored() {
		t.Errorf("expected IsIgnored to be true")
	}
}

func TestNodeWrapper_GetEffectiveMACAddress(t *testing.T) {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				nodeops.AnnotationMACManual: "11:22:33:44:55:66",
				nodeops.AnnotationMACAuto:   "aa:bb:cc:dd:ee:ff",
			},
		},
	}
	wrapper := nodeops.NewNodeWrapper(n, nil, time.Now(), nodeops.NodeAnnotationConfig{MAC: nodeops.AnnotationMACAuto}, nil)
	mac := wrapper.GetEffectiveMACAddress()
	if mac != "11:22:33:44:55:66" {
		t.Errorf("expected manual MAC override to be returned, got: %s", mac)
	}
}

func TestNodeWrapper_HasManualMACOverride(t *testing.T) {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				nodeops.AnnotationMACManual: "11:22:33:44:55:66",
			},
		},
	}
	w := nodeops.NewNodeWrapper(n, nil, time.Now(), nodeops.NodeAnnotationConfig{}, nil)
	if !w.HasManualMACOverride() {
		t.Errorf("expected HasManualMACOverride to return true")
	}
}

func TestNodeWrapper_HasDiscoveredMACAddr(t *testing.T) {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				nodeops.AnnotationMACAuto: "aa:bb:cc:dd:ee:ff",
			},
		},
	}
	w := nodeops.NewNodeWrapper(n, nil, time.Now(), nodeops.NodeAnnotationConfig{}, nil)
	if !w.HasDiscoveredMACAddr() {
		t.Errorf("expected HasDiscoveredMACAddr to return true")
	}
}

func TestNodeWrapper_SetDiscoveredMAC_DryRun(t *testing.T) {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "dry-node"},
	}
	client := fake.NewSimpleClientset(n)

	w := nodeops.NewNodeWrapper(n, nil, time.Now(), nodeops.NodeAnnotationConfig{}, nil)
	err := w.SetDiscoveredMAC(context.Background(), client, "aa:bb:cc:dd:ee:ff", true)
	if err != nil {
		t.Errorf("expected no error in dry-run, got: %v", err)
	}
}

func TestNodeWrapper_SetDiscoveredMAC_ActualPatch(t *testing.T) {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "real-node"},
	}
	client := fake.NewSimpleClientset(n)

	patched := false
	client.Fake.PrependReactor("patch", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchAction).GetPatch()
		if strings.Contains(string(patch), "aa:bb:cc:dd:ee:ff") {
			patched = true
		}
		return false, nil, nil
	})

	w := nodeops.NewNodeWrapper(n, nil, time.Now(), nodeops.NodeAnnotationConfig{}, nil)
	err := w.SetDiscoveredMAC(context.Background(), client, "aa:bb:cc:dd:ee:ff", false)
	if err != nil {
		t.Errorf("expected no error when patching, got: %v", err)
	}
	if !patched {
		t.Error("expected patch to contain the MAC address")
	}
}

func TestNodeWrapper_IsInShutdownCooldown(t *testing.T) {
	tracker := nodeops.NewNodeStateTracker()
	n := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}

	now := time.Now()
	tracker.MarkShutdown("test-node")
	tracker.SetShutdownTime("test-node", now.Add(-2*time.Minute))

	wrapper := nodeops.NewNodeWrapper(n, tracker, now, nodeops.NodeAnnotationConfig{}, nil)

	if !wrapper.IsInShutdownCooldown(5 * time.Minute) {
		t.Error("expected node to be in shutdown cooldown")
	}
	if wrapper.IsInShutdownCooldown(1 * time.Minute) {
		t.Error("expected node to be outside shutdown cooldown")
	}
}

func TestNodeWrapper_IsInBootCooldown(t *testing.T) {
	tracker := nodeops.NewNodeStateTracker()
	n := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "boot-node"}}

	now := time.Now()
	tracker.MarkBooted("boot-node")
	tracker.SetBootTime("boot-node", now.Add(-1*time.Minute))

	wrapper := nodeops.NewNodeWrapper(n, tracker, now, nodeops.NodeAnnotationConfig{}, nil)

	if !wrapper.IsInBootCooldown(5 * time.Minute) {
		t.Error("expected node to be in boot cooldown")
	}
	if wrapper.IsInBootCooldown(30 * time.Second) {
		t.Error("expected node to be outside boot cooldown")
	}
}
