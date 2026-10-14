package core

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
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
	since  int64    // unix seconds it was first seen down
	hidden bool     // reached laneHideAfter (logged once)
	note   laneNote // what it was, for the alert when it is back
}

// lanesDown is what is down on each server and since when.
type lanesDown struct {
	mu     sync.Mutex
	server map[int64]map[string]*laneDown
}

// laneChange is a lane crossing in or out of the subscriptions: down once it has been
// down for laneHideAfter, back when it is up again after that.
type laneChange struct {
	server int64
	key    string
	down   bool
	since  int64 // when it went down, unix seconds
	note   laneNote
}

// laneRound is one look at the servers: what is down on each server looked at, the
// servers kept as they were (offline nodes — their lanes are judged when they report
// again), and which lanes are still switched on.
type laneRound struct {
	down  map[int64][]string
	notes map[string]laneNote
	keep  map[int64]bool
	// wanted reports whether a lane is still switched on; a lane that stops being down
	// because it was switched off or deleted is not "back".
	wanted func(server int64, key string) bool
}

// observe records what is down now: a key seen for the first time starts its clock, a
// key no longer down is forgotten (and so shown again), a server neither looked at nor
// kept is forgotten whole. Returns the lanes that crossed either way.
func (d *lanesDown) observe(now time.Time, r laneRound) []laneChange {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.server == nil {
		d.server = map[int64]map[string]*laneDown{}
	}
	down, notes := r.down, r.notes
	var changes []laneChange
	for id := range d.server {
		if _, ok := down[id]; !ok && !r.keep[id] {
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
			if n, ok := notes[noteKey(id, k)]; ok {
				l.note = n
			}
			if !l.hidden && now.Unix()-l.since >= int64(laneHideAfter/time.Second) {
				l.hidden = true
				logWarn("subscriptions: lane down, left out until it is back", "server", id, "lane", k)
				changes = append(changes, laneChange{server: id, key: k, down: true, since: l.since, note: l.note})
			}
		}
		for k, l := range cur {
			if !seen[k] {
				if l.hidden && (r.wanted == nil || r.wanted(id, k)) {
					logInfo("subscriptions: lane back", "server", id, "lane", k)
					changes = append(changes, laneChange{server: id, key: k, since: l.since, note: l.note})
				}
				delete(cur, k)
			}
		}
	}
	return changes
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

// laneNote is what an alert says about one down lane: whose it is and why.
type laneNote struct {
	server string // label, as the other server alerts write it
	lane   string
	reason string
}

// observeLanes takes stock of what is down on the master and on every node that is
// reporting, and tells the admins about the lanes that crossed. Runs on the health
// loop.
func (m *Manager) observeLanes() { m.observeLanesAt(time.Now()) }

func (m *Manager) observeLanesAt(now time.Time) {
	down := map[int64][]string{model.LocalNodeID: nil}
	notes := map[string]laneNote{} // by noteKey(server, key)

	set, err := m.store.GetSettings()
	if err != nil {
		return
	}
	var local []string
	if set.SubHideOffline && !m.sup.Serving() {
		local = append(local, xrayLanes...)
	}
	// The tunnel is down when the lane is on and it is not running, error or not; a nil
	// device is a build without one, and a restart is covered by laneHideAfter.
	if set.AWGEnabled && set.AWGPort != 0 && m.awg != nil && !m.awg.Running() {
		local = append(local, model.LaneAWG)
		notes[noteKey(model.LocalNodeID, model.LaneAWG)] = laneNote{model.LocalNodeName, "AmneziaWG", m.awg.LastError()}
	}
	if p := m.mieruErr.Load(); set.MieruEnabled && p != nil && *p != "" {
		local = append(local, model.LaneMieru)
		notes[noteKey(model.LocalNodeID, model.LaneMieru)] = laneNote{model.LocalNodeName, "mieru", *p}
	}
	local = append(local, m.turnDownKeys(model.LocalNodeID, model.LocalNodeName, m.turn.Failed(), notes)...)
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
	mieruErr := make(map[int64]string, len(m.nodeMieruErr))
	for id, e := range m.nodeMieruErr {
		mieruErr[id] = e
	}
	m.nodeGeoMu.Unlock()
	keep := map[int64]bool{}
	mieruOn := map[int64]bool{model.LocalNodeID: set.MieruEnabled}
	awgOn := map[int64]bool{model.LocalNodeID: set.AWGEnabled}
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || !n.Joined() {
			continue
		}
		mieruOn[n.ID] = n.MieruEnabled != nil && *n.MieruEnabled
		awgOn[n.ID] = awgEnabledOn(n)
		if !n.Online(now.Unix()) {
			keep[n.ID] = true // judged again when it reports
			continue
		}
		label := nodeLabel(n)
		var keys []string
		if set.SubHideOffline && !n.XrayRunning {
			keys = append(keys, xrayLanes...)
		}
		// Only from an agent that reports the tunnel: silence from an older one is not
		// a tunnel that is down.
		if s := awgState[n.ID]; awgOn[n.ID] && s.Reported && !s.Running {
			keys = append(keys, model.LaneAWG)
			notes[noteKey(n.ID, model.LaneAWG)] = laneNote{label, "AmneziaWG", s.Err}
		}
		if e := mieruErr[n.ID]; e != "" && mieruOn[n.ID] {
			keys = append(keys, model.LaneMieru)
			notes[noteKey(n.ID, model.LaneMieru)] = laneNote{label, "mieru", e}
		}
		keys = append(keys, m.turnDownKeys(n.ID, label, turnDown[n.ID], notes)...)
		down[n.ID] = keys
	}
	wanted := func(server int64, key string) bool {
		switch {
		case key == model.LaneMieru:
			return mieruOn[server]
		case key == model.LaneAWG:
			return awgOn[server]
		case strings.HasPrefix(key, "inbound:"):
			id, err := strconv.ParseInt(strings.TrimPrefix(key, "inbound:"), 10, 64)
			if err != nil {
				return false
			}
			in, err := m.store.GetInbound(id)
			return err == nil && in != nil && in.Enabled && in.ServerID == server
		}
		return true
	}
	round := laneRound{down: down, notes: notes, keep: keep, wanted: wanted}
	for _, msg := range laneAlerts(m.lanesDown.observe(now, round), m.botLang(), now) {
		m.notifyAdminEvent(model.AdminEventXrayDown, msg)
	}
}

func noteKey(server int64, key string) string { return strconv.FormatInt(server, 10) + "/" + key }

// laneAlerts is what to tell the admins about lanes that left or rejoined the
// subscriptions — AmneziaWG, mieru and TURN relays. Xray's lanes are not: Xray down
// has its own alerts (nodeAlertsFor, the Xray watchdog), and saying it twice is noise.
// A lane back carries how long it was out.
func laneAlerts(changes []laneChange, lang i18n.Lang, now time.Time) []string {
	var out []string
	for _, c := range changes {
		if c.key != model.LaneMieru && c.key != model.LaneAWG && !strings.HasPrefix(c.key, "inbound:") {
			continue
		}
		n := c.note
		if n.lane == "" {
			n.lane = c.key
		}
		if c.down {
			msg := fmt.Sprintf(i18n.T(lang, "notify.laneDown"), escHTML(n.lane), n.server)
			if n.reason != "" {
				msg += "\n" + escHTML(n.reason)
			}
			out = append(out, msg)
			continue
		}
		msg := fmt.Sprintf(i18n.T(lang, "notify.laneBack"), escHTML(n.lane), n.server)
		out = append(out, msg+"\n"+i18n.T(lang, "notify.downtime", fmtDowntime(now.Sub(time.Unix(c.since, 0)), lang)))
	}
	return out
}

// turnLaneName is how an alert names a TURN relay's inbound.
func turnLaneName(in model.Inbound) string {
	return in.Name + " (TURN-VK, UDP " + strconv.Itoa(in.Port) + ")"
}

// turnDownKeys is the custom inbounds of a server whose TURN relay could not listen,
// noted for the alert under label.
func (m *Manager) turnDownKeys(serverID int64, label string, ports []int, notes map[string]laneNote) []string {
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
				k := inboundDownKey(in.ID)
				keys = append(keys, k)
				// No reason: the relay knows only that it could not listen there.
				notes[noteKey(serverID, k)] = laneNote{server: label, lane: turnLaneName(in)}
			}
		}
	}
	return keys
}
