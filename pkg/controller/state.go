package controller

import (
	"sync"
	"time"
)

type NodeStateTracker struct {
	mu               sync.Mutex
	recentlyShutdown map[string]time.Time
	poweredOff       map[string]bool
}

func NewNodeStateTracker() *NodeStateTracker {
	return &NodeStateTracker{
		recentlyShutdown: make(map[string]time.Time),
		poweredOff:       make(map[string]bool),
	}
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
