package controller

import (
	"sync"
	"time"
)

type NodeStateTracker struct {
	mu               sync.Mutex
	recentlyShutdown map[string]time.Time
	poweredOff       map[string]bool
	lastShutdownTime time.Time
}

func NewNodeStateTracker() *NodeStateTracker {
	return &NodeStateTracker{
		recentlyShutdown: make(map[string]time.Time),
		poweredOff:       make(map[string]bool),
	}
}

func (n *NodeStateTracker) MarkGlobalShutdown() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastShutdownTime = time.Now()
}

func (n *NodeStateTracker) ClearPoweredOff(nodeName string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.poweredOff, nodeName)
}

func (n *NodeStateTracker) IsGlobalCooldownActive(now time.Time, cooldown time.Duration) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return now.Sub(n.lastShutdownTime) < cooldown
}

func (n *NodeStateTracker) MarkPoweredOff(nodeName string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.poweredOff[nodeName] = true
}

func (n *NodeStateTracker) IsPoweredOff(nodeName string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.poweredOff[nodeName]
}

func (n *NodeStateTracker) MarkShutdown(nodeName string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.recentlyShutdown[nodeName] = time.Now()
}

func (n *NodeStateTracker) IsInCooldown(nodeName string, now time.Time, cooldown time.Duration) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	last, ok := n.recentlyShutdown[nodeName]
	if !ok {
		return false
	}
	return now.Sub(last) < cooldown
}
