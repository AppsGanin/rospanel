package nodeagent

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/shaper"
)

// Per-user speed caps on a node.
//
// The node shapes its own traffic: the panel sends WHO is capped and by how much
// (NodeMeta.SpeedLimits), and the node pairs that with its own access-log tap,
// which is the only place that knows the addresses those users reach THIS server
// from. Sending the master's address view instead would cap addresses that never
// touch this node and miss the ones that do.

// shapeSightingTTL is how long an address keeps a user shaped after their last
// connection through this node. Matches the panel-side window: a cap must not lapse
// because a client paused a download.
const shapeSightingTTL = 10 * time.Minute

// shapeInterval is how often the tree is recomputed. Applying is skipped when the
// state is unchanged, so an idle node does nothing but walk a small map.
const shapeInterval = 30 * time.Second

// maxTrackedAddrs bounds how many addresses one user is tracked on. Larger than
// what is shaped (below) so a roaming client's history isn't thrown away, small
// enough that a forged-email flood can't grow the map.
const maxTrackedAddrs = 64

// seenAddrs remembers which addresses each user has been seen on recently.
//
// Separate from the agent's `conns` buffer even though both are fed by the same
// callback: that one is drained on every sync (it is a report), while this one has
// to keep answering "where is this user now" between syncs. Draining it would take
// the cap off everyone once a minute.
type seenAddrs struct {
	mu   sync.Mutex
	seen map[string]map[string]time.Time // email → ip → last seen
}

func newSeenAddrs() *seenAddrs { return &seenAddrs{seen: map[string]map[string]time.Time{}} }

// note records one sighting.
func (s *seenAddrs) note(email, ip string, now time.Time) {
	// Nil-safe on purpose: this runs from the access-log callback, on every
	// connection the node handles. Shaping is a side feature, and an Agent built
	// without it (tests, and any future partial construction) must not turn the tap
	// into a panic that takes the agent down.
	if s == nil || email == "" || ip == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.seen[email]
	if m == nil {
		// Bounded like the agent's other tap buffers: a flood of forged emails must
		// not grow this without limit. Well above any real user count on one node.
		if len(s.seen) >= 8192 {
			return
		}
		m = map[string]time.Time{}
		s.seen[email] = m
	}
	if _, known := m[ip]; !known && len(m) >= maxTrackedAddrs {
		return // one user on this many addresses inside the TTL is already pathological
	}
	m[ip] = now
}

// rules builds the shaping rules for the given caps, dropping sightings past the
// TTL as it goes — the sweep rides here rather than on a timer of its own because
// this is the only reader, and it runs on exactly the cadence the sweep wants.
func (s *seenAddrs) rules(limits map[string]int, now time.Time) []shaper.Rule {
	if s == nil {
		return nil
	}
	cutoff := now.Add(-shapeSightingTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []shaper.Rule
	for email, ips := range s.seen {
		for ip, at := range ips {
			if at.Before(cutoff) {
				delete(ips, ip)
			}
		}
		if len(ips) == 0 {
			delete(s.seen, email)
			continue
		}
		kbps, capped := limits[email]
		if !capped || kbps <= 0 {
			continue
		}
		id, ok := userIDFromEmail(email)
		if !ok {
			continue
		}
		// Newest first, then bounded: every address becomes a classifier the kernel
		// walks per packet, and the newest ones are where the traffic is. Same bound
		// as the panel applies on its own side, for the same reason.
		addrs := make([]string, 0, len(ips))
		for ip := range ips {
			addrs = append(addrs, ip)
		}
		sort.Slice(addrs, func(i, j int) bool { return ips[addrs[i]].After(ips[addrs[j]]) })
		if len(addrs) > model.MaxShapedIPsPerUser {
			addrs = addrs[:model.MaxShapedIPsPerUser]
		}
		out = append(out, shaper.Rule{UserID: id, Kbps: kbps, IPs: addrs})
	}
	return out
}

// shapeLoop keeps this node's speed caps in step with the panel's limits and the
// node's own view of who is connected from where.
func (a *Agent) shapeLoop(ctx context.Context) {
	t := time.NewTicker(shapeInterval)
	defer t.Stop()
	for {
		a.applyShaping()
		select {
		case <-ctx.Done():
			// Leave the host as we found it: the kernel keeps a qdisc tree until
			// reboot, and an agent that stopped must not keep capping anyone.
			a.shaper.Reset()
			return
		case <-t.C:
		}
	}
}

func (a *Agent) applyShaping() {
	limits := a.speedLimits()
	rules := a.seen.rules(limits, time.Now())
	a.shaper.Apply(shaper.State{WAN: a.wanIface(), Rules: rules})
}

// wanIface resolves the interface to shape once and remembers it. Guarded because
// applyShaping runs both on the shaper's own timer and right after a state apply —
// two goroutines that would otherwise write this field at the same moment.
func (a *Agent) wanIface() string {
	a.wanMu.Lock()
	defer a.wanMu.Unlock()
	if a.wan == "" {
		a.wan = shaper.DefaultWAN()
	}
	return a.wan
}

// speedLimits is the cap table from the last applied state, empty when the panel
// sent none (or is too old to send any).
func (a *Agent) speedLimits() map[string]int {
	meta, ok := a.currentMeta()
	if !ok {
		return nil
	}
	return meta.SpeedLimits
}
