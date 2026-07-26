package core

import (
	"context"
	"sync"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// Port probing on a remote node.
//
// The panel validates a custom inbound's port BEFORE saving it, but it can only bind
// a socket on its own machine. For a node it has to ask, and the only channel to a
// node is the long-poll the node holds open: the panel attaches the request to the
// held response, the node bind-tests and reports the outcome on its next sync.
//
// The registry below is that round trip. A pending probe parks on a channel; the
// sync handler delivers the answer and closes it. Nothing is persisted — a probe is
// only meaningful for the request that asked for it, and an unanswered one degrades
// to "couldn't check" rather than blocking the operator (see Manager.probePort).

// probeWaiter is one caller blocked on one probe. Waiters are tracked individually
// rather than keyed by (network, port): two admins can be validating the same port on
// the same node at the same moment, and a map keyed by the probe would have the
// second registration evict the first — after which the first's deferred cancel
// removes the SECOND's channel, so neither ever hears back.
type probeWaiter struct {
	probe nodeapi.PortProbe
	ch    chan nodeapi.PortProbeResult
	// sent marks that the request has already gone out on a response. The sync
	// handler cuts a node's long-poll short so a pending probe travels at once — but
	// only for a request it hasn't sent yet. Without that distinction an agent too
	// old to answer would never clear the pending entry, every one of its polls would
	// return instantly, and the node would hammer the panel for the whole timeout.
	sent bool
}

// probeRegistry tracks the in-flight port probes of every node.
type probeRegistry struct {
	mu      sync.Mutex
	pending map[int64][]*probeWaiter
}

func newProbeRegistry() *probeRegistry {
	return &probeRegistry{pending: map[int64][]*probeWaiter{}}
}

// add registers a probe and returns the channel its answer will arrive on, plus a
// cancel func the caller must defer so a timed-out probe doesn't leak an entry.
func (r *probeRegistry) add(nodeID int64, p nodeapi.PortProbe) (<-chan nodeapi.PortProbeResult, func()) {
	// Buffered so resolve() never blocks on a caller that has already given up.
	w := &probeWaiter{probe: p, ch: make(chan nodeapi.PortProbeResult, 1)}
	r.mu.Lock()
	r.pending[nodeID] = append(r.pending[nodeID], w)
	r.mu.Unlock()

	return w.ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		list := r.pending[nodeID]
		for i, x := range list {
			if x == w { // identity, so a concurrent waiter on the same port survives
				r.pending[nodeID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(r.pending[nodeID]) == 0 {
			delete(r.pending, nodeID)
		}
	}
}

// wanted lists the probes a node still owes an answer for and marks them as sent.
// Deduplicated, since several waiters may ask about the same port and the node need
// only test it once.
func (r *probeRegistry) wanted(nodeID int64) []nodeapi.PortProbe {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[nodeapi.PortProbe]struct{}{}
	var out []nodeapi.PortProbe
	for _, w := range r.pending[nodeID] {
		w.sent = true
		if _, dup := seen[w.probe]; dup {
			continue
		}
		seen[w.probe] = struct{}{}
		out = append(out, w.probe)
	}
	return out
}

// hasFresh reports whether a probe is waiting that has NOT been sent yet — the only
// case worth cutting a held poll short for.
func (r *probeRegistry) hasFresh(nodeID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.pending[nodeID] {
		if !w.sent {
			return true
		}
	}
	return false
}

// resolve delivers a node's answers to EVERY waiter they match, not just the first.
func (r *probeRegistry) resolve(nodeID int64, results []nodeapi.PortProbeResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, res := range results {
		key := nodeapi.PortProbe{Network: res.Network, Port: res.Port}
		for _, w := range r.pending[nodeID] {
			if w.probe != key {
				continue
			}
			select {
			case w.ch <- res:
			default: // already answered; keep the first
			}
		}
	}
}

// NodeProbePorts returns the port probes a node should run, for the sync handler to
// attach to its response.
//
// Nil-guarded because the package's tests build partial Managers by design; a missing
// registry means "nothing pending", never a panic on the node sync path.
func (m *Manager) NodeProbePorts(nodeID int64) []nodeapi.PortProbe {
	if m.probes == nil {
		return nil
	}
	return m.probes.wanted(nodeID)
}

// NodeHasFreshWork reports whether this node has an unsent probe or config check
// waiting — the signal the sync handler uses to answer at once instead of holding.
func (m *Manager) NodeHasFreshWork(nodeID int64) bool {
	return (m.probes != nil && m.probes.hasFresh(nodeID)) ||
		(m.checks != nil && m.checks.hasFresh(nodeID))
}

// RecordNodeProbeResults delivers a node's probe answers, called from the sync
// handler when the node reports them.
func (m *Manager) RecordNodeProbeResults(nodeID int64, results []nodeapi.PortProbeResult) {
	if m.probes != nil && len(results) > 0 {
		m.probes.resolve(nodeID, results)
	}
}

// ProbeNodePort asks a node whether a port is free and waits for the answer.
//
// Returns errProbeUnavailable when the node doesn't answer in time — offline, or
// running an agent that predates this request. The caller treats that as "couldn't
// check" and lets the save through: refusing to configure a temporarily unreachable
// server would be a worse failure than the one being guarded against, and the node
// still validates and rolls back a config its Xray can't start.
func (m *Manager) ProbeNodePort(ctx context.Context, nodeID int64, network string, port int) (bool, error) {
	n, err := m.store.GetNode(nodeID)
	if err != nil {
		return false, err
	}
	if n == nil {
		return false, invalid("сервер не найден")
	}
	if !n.Online(nowUnix()) || m.probes == nil {
		return false, errProbeUnavailable
	}

	p := nodeapi.PortProbe{Network: network, Port: port}
	ch, cancel := m.probes.add(nodeID, p)
	defer cancel()

	// Wake the node so its held poll returns at once and carries the request, instead
	// of waiting out the remainder of the hold.
	m.nodes.wakeOne(nodeID)

	ctx, stop := context.WithTimeout(ctx, inboundProbeTimeout)
	defer stop()
	select {
	case res := <-ch:
		return res.Free, nil
	case <-ctx.Done():
		return false, errProbeUnavailable
	}
}
