package core

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// A node's report is taken on trust in what it says about whom: traffic, connections
// and destinations each name a user. A node the operator no longer controls could name
// anyone — spend a stranger's quota, push them over their device limit, get their
// address banned fleet-wide, have them cut off for "abuse", start a held term. So a
// node is believed only about the users its config lets in: the ones the panel gave
// it. What it can still do to those, it could do by serving them badly anyway.

// nodeServedGrace is how long a user who has left a node's config is still believed
// in its reports. The push that removes someone is built before the node applies it,
// and what they used until then arrives on the node's next syncs — or, after an
// outage between the two, with its backlog.
const nodeServedGrace = time.Hour

// nodeServed is who one node's last built state lets in. Never changed once shared: a
// new build replaces it whole, so a reader holds a consistent one without the lock.
type nodeServed struct {
	ids  []int64         // sorted, no repeats
	left map[int64]int64 // user id → unix second their last state without them was built
	// unread: the config could not be read for its users (see xray.Config.ClientEmails).
	// Nothing is checked then — refusing everything would silently stop counting the
	// node's traffic over a reader that fell behind the generator — and it is logged.
	unread bool
	// seq orders the reads states are built from (see servedRegistry.stamp).
	seq uint64
}

// allows reports whether a report from the node may speak for this user.
func (s *nodeServed) allows(id int64, now int64) bool {
	if s.unread {
		return true
	}
	if _, found := slices.BinarySearch(s.ids, id); found {
		return true
	}
	at, ok := s.left[id]
	return ok && now-at <= int64(nodeServedGrace/time.Second)
}

// servedRegistry holds each node's nodeServed. Its zero value is ready to use.
type servedRegistry struct {
	mu    sync.Mutex
	nodes map[int64]*nodeServed
	reads atomic.Uint64
}

// stamp numbers a read of a node's state inputs, taken before the read. States for one
// node are built side by side — a sync's, the config viewer's, the health report's — and
// the one that finishes last is not always the one read last: without the number, a
// state built from older inputs could replace a newer one's users, and the node's reports
// about someone it was just given would be dropped until the next build.
func (r *servedRegistry) stamp() uint64 { return r.reads.Add(1) }

func (r *servedRegistry) get(nodeID int64) *nodeServed {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nodes[nodeID]
}

// note records the users a state just built for a node lets in, from inputs read under
// stamp seq; a state read before the one already recorded is ignored. Users of the
// previous state who are not in this one are remembered as having left now; those who
// left earlier are kept until the grace runs out, and forgotten as soon as they are back.
func (r *servedRegistry) note(nodeID int64, ids []int64, unread bool, now int64, seq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nodes == nil {
		r.nodes = map[int64]*nodeServed{}
	}
	prev := r.nodes[nodeID]
	if prev != nil && seq < prev.seq {
		return
	}
	next := &nodeServed{ids: ids, unread: unread, seq: seq}
	if prev != nil {
		grace := int64(nodeServedGrace / time.Second)
		left := make(map[int64]int64)
		for id, at := range prev.left {
			if now-at <= grace {
				left[id] = at
			}
		}
		// A state that could not be read says nothing about who left it.
		if !prev.unread && !unread {
			for _, id := range prev.ids {
				if _, still := slices.BinarySearch(ids, id); !still {
					left[id] = now
				}
			}
		}
		for _, id := range ids {
			delete(left, id)
		}
		if len(left) > 0 {
			next.left = left
		}
	}
	r.nodes[nodeID] = next
}

func (r *servedRegistry) forget(nodeID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, nodeID)
}

// servedUserIDs lists the users a node's state lets in: everyone on an inbound of its
// Xray config, and every peer of its AmneziaWG tunnel. readable is false when the
// config holds users this cannot read.
func servedUserIDs(cfg *xray.Config, tunnel *nodeapi.AWGState) (ids []int64, readable bool) {
	emails, readable := cfg.ClientEmails()
	if tunnel != nil {
		for _, p := range tunnel.Peers {
			emails = append(emails, p.Email)
		}
	}
	ids = make([]int64, 0, len(emails))
	for _, e := range emails {
		// Not every entry is a user: a Shadowsocks inbound nobody may use carries a
		// locked entry of its own.
		if id, ok := userIDFromEmail(e); ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), readable
}

// noteNodeServed records who a state just built for a node lets in, from inputs read
// under stamp seq.
func (m *Manager) noteNodeServed(nodeID int64, cfg *xray.Config, tunnel *nodeapi.AWGState, seq uint64) {
	ids, readable := servedUserIDs(cfg, tunnel)
	if !readable && m.siteNotice.should(fmt.Sprintf("node-served-unread:%d", nodeID), time.Now()) {
		logErr("node state: the config's users cannot be read, so the node's reports are not checked against them",
			"node", nodeID)
	}
	m.served.note(nodeID, ids, !readable, time.Now().Unix(), seq)
}

// nodeServedFor returns who a node's state lets in. A node no state has been built for
// since the panel started — its first sync after a restart — gets one built now; that
// build is remembered like any other, so the sync that follows need not build again.
func (m *Manager) nodeServedFor(n *model.Node) (*nodeServed, error) {
	if s := m.served.get(n.ID); s != nil {
		return s, nil
	}
	_, done, err := m.NodeStatePush(context.Background(), n, "")
	done()
	if err != nil {
		return nil, err
	}
	if s := m.served.get(n.ID); s != nil {
		return s, nil
	}
	return nil, fmt.Errorf("no state was built for node %d", n.ID)
}
