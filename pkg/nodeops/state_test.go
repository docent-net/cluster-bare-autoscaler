package nodeops_test

import (
	"testing"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
)

func TestNodeStateTracker_PoweredOff(t *testing.T) {
	s := nodeops.NewNodeStateTracker()

	s.MarkPoweredOff("node1")
	if !s.IsPoweredOff("node1") {
		t.Errorf("expected node1 to be powered off")
	}

	s.ClearPoweredOff("node1")
	if s.IsPoweredOff("node1") {
		t.Errorf("expected node1 to be cleared from powered off state")
	}
}

func TestNodeStateTracker_Cooldowns(t *testing.T) {
	s := nodeops.NewNodeStateTracker()
	s.MarkShutdown("node1")
	if !s.IsInCooldown("node1", time.Now(), time.Minute) {
		t.Errorf("expected node1 to be in shutdown cooldown")
	}

	s.MarkBooted("node2")
	if !s.IsBootCooldownActive("node2", time.Now(), time.Minute) {
		t.Errorf("expected node2 to be in boot cooldown")
	}

	s.MarkGlobalShutdown()
	if !s.IsGlobalCooldownActive(time.Now(), time.Minute) {
		t.Errorf("expected global cooldown to be active")
	}
}
