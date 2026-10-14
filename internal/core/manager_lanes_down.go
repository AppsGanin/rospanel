package core

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Lanes that are down are left out of subscriptions.
//
// Only what a server knows about itself counts: the AmneziaWG tunnel failing to
// start, a TURN relay failing to listen, Xray not serving. Those are certain — a
// client cannot connect to a listener that is not there — unlike anything judged from
// outside, which can be wrong and would then take a working lane away. A lane is left
// out once it has been down for laneHideAfter without a break, so a restart is never
// noticed, and comes back as soon as it is up.
//
// Xray down is the whole server down, and that is left out only under SubHideOffline,
// as a node that stopped reporting is: a client refreshes its subscription hours
// apart, so one that refreshed during the outage would go on without the server long
// after it is back, while a dead entry costs it nothing. A lane that fails while the
// rest of the server works leaves the server in the subscription either way.

// laneHideAfter is how long a lane has to stay down before subscriptions drop it.
const laneHideAfter = 2 * time.Minute

// xrayLanes are the built-in lanes that need Xray: Naive, AnyTLS and mieru end in an
// Xray inbound too.
var xrayLanes = []string{model.LaneVLESS, model.LaneReality, model.LaneHysteria,
	model.LaneNaive, model.LaneAnyTLS, model.LaneMieru, model.SubProtoCustom}

// inboundDownKey is the key of one custom inbound in lanesDown.
func inboundDownKey(id int64) string { return "inbound:" + strconv.FormatInt(id, 10) }

type laneDown struct {
	since  int64 // unix seconds it was first seen down
	hidden bool  // reached laneHideAfter (logged once)
}

// lanesDown is what is down on each server and since when.
type lanesDown struct {
	mu     sync.Mutex
	server map[int64]map[string]*laneDown
}

// observe records what is down now: a key seen for the first time starts its clock, a
// key no longer down is forgotten (and so shown again), a server not in now is
// forgotten whole.
func (d *lanesDown) observe(now time.Time, down map[int64][]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.server == nil {
		d.server = map[int64]map[string]*laneDown{}
	}
	for id, keys := range d.server {
		if _, ok := down[id]; !ok {
			for k, l := range keys {
				if l.hidden {
					logInfo("subscriptions: lane back", "server", id, "lane", k)
				}
			}
			delete(d.server, id)
		}
	}
	for id, keys := range down {
		cur := d.server[id]
		if cur == nil {
			cur = map[string]*laneDown{}
			d.server[id] = cur
		}
		seen := map[string]bool{}
		for _, k := range keys {
			seen[k] = true
			l := cur[k]
			if l == nil {
				l = &laneDown{since: now.Unix()}
				cur[k] = l
			}
			if !l.hidden && now.Unix()-l.since >= int64(laneHideAfter/time.Second) {
				l.hidden = true
				logWarn("subscriptions: lane down, left out until it is back", "server", id, "lane", k)
			}
		}
		for k, l := range cur {
			if !seen[k] {
				if l.hidden {
					logInfo("subscriptions: lane back", "server", id, "lane", k)
				}
				delete(cur, k)
			}
		}
	}
}

// hiddenOn is the lanes and the custom inbounds of one server to leave out.
func (d *lanesDown) hiddenOn(server int64) (map[string]bool, map[int64]bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var lanes map[string]bool
	var inbounds map[int64]bool
	for k, l := range d.server[server] {
		if !l.hidden {
			continue
		}
		if s, ok := strings.CutPrefix(k, "inbound:"); ok {
			if id, err := strconv.ParseInt(s, 10, 64); err == nil {
				if inbounds == nil {
					inbounds = map[int64]bool{}
				}
				inbounds[id] = true
			}
			continue
		}
		if lanes == nil {
			lanes = map[string]bool{}
		}
		lanes[k] = true
	}
	return lanes, inbounds
}

// DownLanes is what subscriptions leave out of a server: built-in lanes by key (the
// custom inbounds as a whole as model.SubProtoCustom) and single custom inbounds by ID.
func (m *Manager) DownLanes(serverID int64) (map[string]bool, map[int64]bool) {
	return m.lanesDown.hiddenOn(serverID)
}

// observeLanes takes stock of what is down on the master and on every node that is
// reporting. Runs on the health loop.
func (m *Manager) observeLanes() {
	now := time.Now()
	down := map[int64][]string{model.LocalNodeID: nil}

	set, err := m.store.GetSettings()
	if err != nil {
		return
	}
	var local []string
	if set.SubHideOffline && !m.sup.Serving() {
		local = append(local, xrayLanes...)
	}
	if set.AWGEnabled && set.AWGPort != 0 && m.awg != nil && !m.awg.Running() && m.awg.LastError() != "" {
		local = append(local, model.LaneAWG)
	}
	local = append(local, m.turnDownKeys(model.LocalNodeID, m.turn.Failed())...)
	down[model.LocalNodeID] = local

	nodes, err := m.store.ListNodes()
	if err != nil {
		return
	}
	m.nodeGeoMu.Lock()
	awgState := make(map[int64]nodeAWGState, len(m.nodeAWG))
	for id, s := range m.nodeAWG {
		awgState[id] = s
	}
	turnDown := make(map[int64][]int, len(m.nodeTurnDown))
	for id, p := range m.nodeTurnDown {
		turnDown[id] = p
	}
	m.nodeGeoMu.Unlock()
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || !n.Joined() || !n.Online(now.Unix()) {
			continue
		}
		var keys []string
		if set.SubHideOffline && !n.XrayRunning {
			keys = append(keys, xrayLanes...)
		}
		if s := awgState[n.ID]; s.Reported && !s.Running && s.Err != "" {
			keys = append(keys, model.LaneAWG)
		}
		keys = append(keys, m.turnDownKeys(n.ID, turnDown[n.ID])...)
		down[n.ID] = keys
	}
	m.lanesDown.observe(now, down)
}

// turnDownKeys is the custom inbounds of a server whose TURN relay could not listen.
func (m *Manager) turnDownKeys(serverID int64, ports []int) []string {
	if len(ports) == 0 {
		return nil
	}
	list, err := m.store.EnabledInbounds(serverID)
	if err != nil {
		return nil
	}
	var keys []string
	for _, in := range list {
		if in.Protocol != model.InbTurnVK {
			continue
		}
		for _, p := range ports {
			if in.Port == p {
				keys = append(keys, inboundDownKey(in.ID))
			}
		}
	}
	return keys
}
