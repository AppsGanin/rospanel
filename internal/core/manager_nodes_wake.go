package core

import (
	"sync"
	"sync/atomic"
)

// --- node wake registry -------------------------------------------------------
//
// Each connected node's sync handler parks on a wake channel; a config change
// (user add/remove, node edit) closes it so the held poll returns immediately and
// re-pushes the fresh desired state. Panels with no connected nodes pay nothing.

type nodeRegistry struct {
	mu    sync.Mutex
	waits map[int64]chan struct{}
	// gen counts wakes. A wake is how a change reaches the nodes, so it is also what
	// invalidates the inputs their desired state was built from (see nodeInputs).
	gen atomic.Uint64
}

func newNodeRegistry() *nodeRegistry { return &nodeRegistry{waits: map[int64]chan struct{}{}} }

// wakeChan returns the current wake channel for a node, creating it on first use.
func (r *nodeRegistry) wakeChan(nodeID int64) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.waits[nodeID]
	if !ok {
		ch = make(chan struct{})
		r.waits[nodeID] = ch
	}
	return ch
}

// wakeOne closes and replaces one node's wake channel (any parked poll returns and
// re-parks on the fresh channel). It only acts on an existing entry: a poll always
// registers its channel via wakeChan before computing desired state, so there is
// nothing to wake until then — and not creating entries here keeps the map from
// accumulating channels for nodes that never poll.
func (r *nodeRegistry) wakeOne(nodeID int64) {
	if r == nil {
		return // no registry (tests) ⇒ no parked poll to wake
	}
	r.gen.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.waits[nodeID]; ok {
		close(ch)
		r.waits[nodeID] = make(chan struct{})
	}
}

// dropWaiter wakes and removes a node's entry (used on delete, so a tombstoned
// node's channel isn't retained forever).
func (r *nodeRegistry) dropWaiter(nodeID int64) {
	r.gen.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.waits[nodeID]; ok {
		close(ch)
		delete(r.waits, nodeID)
	}
}

// wakeAll wakes every parked node — used after a user-set change that fans out to
// all nodes. Nil-safe: a Manager assembled without a registry (tests) still has to
// survive the paths that now reach here, and "no registry" means "no node to wake".
func (r *nodeRegistry) wakeAll() {
	if r == nil {
		return
	}
	r.gen.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, ch := range r.waits {
		close(ch)
		r.waits[id] = make(chan struct{})
	}
}

// parked is how many nodes have a poll waiting. None means nothing to wake, and so
// nothing to work out about what they would be woken for.
func (r *nodeRegistry) parked() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.waits)
}

// generation is the registry's wake count; 0 without a registry.
func (r *nodeRegistry) generation() uint64 {
	if r == nil {
		return 0
	}
	return r.gen.Load()
}

// NodeWakeChan exposes a node's wake channel to the sync handler.
func (m *Manager) NodeWakeChan(nodeID int64) <-chan struct{} { return m.nodes.wakeChan(nodeID) }

// notifyNodes wakes all connected nodes so they re-pull desired state. Called
// after every reconcile/user-sync and after node edits.
func (m *Manager) notifyNodes() { m.nodes.wakeAll() }
