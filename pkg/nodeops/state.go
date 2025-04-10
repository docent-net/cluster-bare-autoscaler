// Package state manages cooldown tracking and power state for each node.
//
// Overview:
// The NodeStateTracker holds in-memory state to coordinate cooldowns and power transitions for nodes.
// This state is *ephemeral* and does not persist across restarts of the autoscaler.
// It includes timestamps and flags used to decide if a node can be shut down or powered on again.
//
// Cooldown flow explained:
// 1. **Global Cooldown**:
//    - Starts when any node is shut down (scale-down) or powered on (scale-up).
//    - Prevents *any* further scaling actions (both up and down) across all nodes.
//    - Duration is configured via `cooldown` in `config.yaml`.
//    - Tracked using `LastShutdownTime`.
//
// 2. **Per-node Cooldowns**:
//    - These prevent excessive churning by rate-limiting actions on individual nodes.
//
//    a. **Shutdown Cooldown**:
//       - Prevents shutting down the same node again too soon.
//       - Set via `MarkShutdown(node)` and checked with `IsInCooldown(...)`.
//       - Controlled via the global `cooldown` config.
//
//    b. **Boot Cooldown**:
//       - Prevents newly powered-on nodes from being shut down immediately.
//       - Set via `MarkBooted(node)` and checked with `IsBootCooldownActive(...)`.
//       - Controlled via a separate `bootCooldown` config (e.g., `bootCooldownSeconds`).
//
// Additional tracking:
// - `poweredOff` tracks which nodes are currently considered powered off.
//   This is a temporary, in-memory view used by the autoscaler to avoid re-powering nodes
//   during the same runtime session. It is cleared upon scale-up.

package nodeops

import (
	"sync"
	"time"
)

// NodeStateTracker keeps track of node cooldowns and powered-off state.
type NodeStateTracker struct {
	mu                 sync.Mutex
	shutdownTimestamps map[string]time.Time
	bootTimestamps     map[string]time.Time
	poweredOff         map[string]struct{}
	LastShutdownTime   time.Time
}

// NewNodeStateTracker initializes all internal maps for tracking.
func NewNodeStateTracker() *NodeStateTracker {
	return &NodeStateTracker{
		shutdownTimestamps: make(map[string]time.Time),
		bootTimestamps:     make(map[string]time.Time),
		poweredOff:         make(map[string]struct{}),
	}
}

// MarkShutdown stores the timestamp when the node was shut down.
func (s *NodeStateTracker) MarkShutdown(node string) {
	s.shutdownTimestamps[node] = time.Now()
}

// IsInCooldown returns true if the node is still within shutdown cooldown period.
func (s *NodeStateTracker) IsInCooldown(node string, now time.Time, cooldown time.Duration) bool {
	last, ok := s.shutdownTimestamps[node]
	if !ok {
		return false
	}
	return now.Sub(last) < cooldown
}

// MarkPoweredOff registers the node as currently powered off.
func (s *NodeStateTracker) MarkPoweredOff(node string) {
	s.poweredOff[node] = struct{}{}
}

// ClearPoweredOff removes the powered-off state for a node.
func (s *NodeStateTracker) ClearPoweredOff(node string) {
	delete(s.poweredOff, node)
}

// IsPoweredOff returns true if the node is marked as powered off.
func (s *NodeStateTracker) IsPoweredOff(node string) bool {
	_, ok := s.poweredOff[node]
	return ok
}

// MarkGlobalShutdown sets the timestamp for the last global scale-up/down action.
// This is used to enforce the global cooldown across all nodes.
func (s *NodeStateTracker) MarkGlobalShutdown() {
	s.LastShutdownTime = time.Now()
}

// IsGlobalCooldownActive returns true if the current time is still within global cooldown window.
func (s *NodeStateTracker) IsGlobalCooldownActive(now time.Time, cooldown time.Duration) bool {
	return now.Sub(s.LastShutdownTime) < cooldown
}

// MarkBooted stores the timestamp when the node was powered on.
func (s *NodeStateTracker) MarkBooted(node string) {
	s.bootTimestamps[node] = time.Now()
}

// IsBootCooldownActive returns true if the node was recently powered on and still within boot cooldown.
func (s *NodeStateTracker) IsBootCooldownActive(node string, now time.Time, cooldown time.Duration) bool {
	last, ok := s.bootTimestamps[node]
	if !ok {
		return false
	}
	return now.Sub(last) < cooldown
}

func (s *NodeStateTracker) SetShutdownTime(nodeName string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shutdownTimestamps[nodeName] = t
}
