package core

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/Shu1t3/rospanel-shu1t3/internal/auth"
	"github.com/Shu1t3/rospanel-shu1t3/internal/nodeapi"
)

// Validating a candidate config on a remote node.
//
// The panel can generate a node's config but cannot judge it: whether Xray accepts a
// given advanced setting is decided by Xray's own parser, and the only copy that
// matters runs on the node. So the candidate rides the long-poll out, the node runs
// `xray run -test` WITHOUT applying it, and the verdict rides the next sync back.
//
// Same shape as the port probe next door, and for the same reason — without it the
// operator learns about a bad setting from a crashed Xray and a rollback, a second of
// downtime for every user on that server.

// checkWaiter is one caller blocked on one config check, keyed by a random id so a
// late answer to a superseded question can never satisfy a newer one.
type checkWaiter struct {
	id  string
	ch  chan nodeapi.ConfigCheckResult
	req nodeapi.ConfigCheckRequest
	// sent marks that the candidate has already gone out — see probeWaiter.sent for
	// why the distinction matters.
	sent bool
}

// checkRegistry tracks the in-flight config checks of every node.
type checkRegistry struct {
	mu      sync.Mutex
	pending map[int64][]*checkWaiter
}

func newCheckRegistry() *checkRegistry {
	return &checkRegistry{pending: map[int64][]*checkWaiter{}}
}

// add registers a check and returns its result channel plus a cancel func.
func (r *checkRegistry) add(nodeID int64, req nodeapi.ConfigCheckRequest) (<-chan nodeapi.ConfigCheckResult, func()) {
	w := &checkWaiter{id: req.ID, ch: make(chan nodeapi.ConfigCheckResult, 1), req: req}
	r.mu.Lock()
	r.pending[nodeID] = append(r.pending[nodeID], w)
	r.mu.Unlock()

	return w.ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		list := r.pending[nodeID]
		for i, x := range list {
			if x == w {
				r.pending[nodeID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(r.pending[nodeID]) == 0 {
			delete(r.pending, nodeID)
		}
	}
}

// wanted returns the check a node should run next, or nil.
//
// One at a time on purpose: a config is the largest thing this channel carries, and
// two operators saving at once would otherwise put both candidates on one response.
// The second waiter is served by the following round trip.
func (r *checkRegistry) wanted(nodeID int64) *nodeapi.ConfigCheckRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if list := r.pending[nodeID]; len(list) > 0 {
		list[0].sent = true
		req := list[0].req
		return &req
	}
	return nil
}

// hasFresh reports whether a candidate is waiting that has not been sent yet.
func (r *checkRegistry) hasFresh(nodeID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.pending[nodeID] {
		if !w.sent {
			return true
		}
	}
	return false
}

// resolve delivers a verdict to the waiter whose id it carries.
func (r *checkRegistry) resolve(nodeID int64, res nodeapi.ConfigCheckResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.pending[nodeID] {
		if w.id != res.ID {
			continue
		}
		select {
		case w.ch <- res:
		default: // already answered; keep the first
		}
	}
}

// NodeConfigCheck returns the config a node should validate on this response.
func (m *Manager) NodeConfigCheck(nodeID int64) *nodeapi.ConfigCheckRequest {
	if m.checks == nil {
		return nil
	}
	return m.checks.wanted(nodeID)
}

// RecordNodeConfigCheck delivers a node's verdict, called from the sync handler.
func (m *Manager) RecordNodeConfigCheck(nodeID int64, res *nodeapi.ConfigCheckResult) {
	if m.checks != nil && res != nil && res.ID != "" {
		m.checks.resolve(nodeID, *res)
	}
}

// checkNodeConfig asks a node to validate a candidate config and waits for the
// verdict. Returns nil when the node accepts it, a validation error when Xray
// refuses, and errProbeUnavailable when the node can't answer — offline, or running
// an agent that predates this request.
//
// "Can't answer" is not "invalid", for the same reason as the port probe: refusing to
// configure a temporarily unreachable server is a worse failure than the one being
// guarded against, and the node still validates and rolls back on apply.
func (m *Manager) checkNodeConfig(ctx context.Context, nodeID int64, cfg json.RawMessage) error {
	n, err := m.store.GetNode(nodeID)
	if err != nil {
		return err
	}
	if n == nil || !n.Online(nowUnix()) {
		return errProbeUnavailable
	}
	id, err := auth.RandomToken()
	if err != nil {
		return err
	}
	if m.checks == nil {
		return errProbeUnavailable
	}
	req := nodeapi.ConfigCheckRequest{ID: id, Config: cfg}
	ch, cancel := m.checks.add(nodeID, req)
	defer cancel()

	// Wake the node so its held poll returns at once carrying the request.
	m.nodes.wakeOne(nodeID)

	ctx, stop := context.WithTimeout(ctx, nodeCheckTimeout)
	defer stop()
	select {
	case res := <-ch:
		if res.OK {
			return nil
		}
		return invalidCode("err.nodeXrayRejectedConfig", "Xray на этом сервере отклонил конфигурацию: {{err}}", map[string]any{"err": res.Err})
	case <-ctx.Done():
		return errProbeUnavailable
	}
}
