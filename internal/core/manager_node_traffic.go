package core

import (
	"fmt"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Per-server traffic caps.
//
// Hosting sells bandwidth by the month. A server that runs through its allowance is
// throttled to uselessness or billed as overage, and the operator finds out from the
// hosting panel days later — while the panel has counted every byte through that
// server all along. This is the comparison against the operator's own threshold, plus
// what the two consumers need: an alert when it is crossed, and a subscription that
// can stop handing out a server which has run out of allowance.
//
// The usage is CACHED rather than queried per request. The subscription path asks on
// every client refresh, and the answer is a SUM over traffic_daily across every user
// and every day of the month — cheap once a minute, not cheap thousands of times an
// hour. The node watch loop already runs on that cadence and already raises the other
// per-server alerts, so it owns the refresh.

// nodeTrafficUsage is one server's answer: bytes carried in the current period, and
// whether that has reached its cap.
type nodeTrafficUsage struct {
	Used int64
	Over bool
}

// nodeTrafficCache is the whole fleet's answer, replaced wholesale on each refresh.
type nodeTrafficCache struct {
	mu   sync.RWMutex
	byID map[int64]nodeTrafficUsage
}

func (c *nodeTrafficCache) get(id int64) nodeTrafficUsage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.byID[id]
}

func (c *nodeTrafficCache) overSet() map[int64]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[int64]bool, len(c.byID))
	for id, u := range c.byID {
		if u.Over {
			out[id] = true
		}
	}
	return out
}

func (c *nodeTrafficCache) replace(v map[int64]nodeTrafficUsage) {
	c.mu.Lock()
	c.byID = v
	c.mu.Unlock()
}

// trafficPeriodStart is the first day of the window a cap is measured over, as the
// YYYY-MM-DD key traffic_daily is bucketed by. In the operator's timezone, because
// that is the day boundary every other number in the panel already uses.
func trafficPeriodStart(period string, now time.Time) string {
	if model.TrafficPeriodOr(period) == model.TrafficDay {
		return now.Format("2006-01-02")
	}
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
}

// NodeTrafficUsage reports what one server has carried in its cap period and whether
// it is over. A server with no cap reports its month-to-date usage and Over false, so
// the UI can show the figure before a cap is ever set.
func (m *Manager) NodeTrafficUsage(id int64) nodeTrafficUsage { return m.nodeTraffic.get(id) }

// ServersOverTrafficLimit is the set of servers that have reached their cap. It is a
// fact, not a decision: whether being over means dropping out of a subscription is the
// server's own HideWhenOver, which sub.Order reads from the placement alongside it —
// the same split capacity already uses (a full server is only hidden if HideWhenFull).
func (m *Manager) ServersOverTrafficLimit() map[int64]bool { return m.nodeTraffic.overSet() }

// refreshNodeTraffic recomputes the whole fleet's period usage. Called from the node
// watch loop, and once at startup so the first subscription after a restart is not
// answered from an empty cache.
func (m *Manager) refreshNodeTraffic() {
	set, err := m.store.GetSettings()
	if err != nil {
		logErr("node traffic: cannot read settings", "err", err)
		return
	}
	nodes, err := m.store.ListNodes()
	if err != nil {
		logErr("node traffic: cannot list nodes", "err", err)
		return
	}
	now := time.Now().In(m.loc())
	today := now.Format("2006-01-02")

	// Placements keyed by server, master included. The master is not in ListNodes —
	// it is the virtual node 0 whose placement lives in settings.
	places := map[int64]model.Placement{model.LocalNodeID: set.MasterPlacement}
	for i := range nodes {
		places[nodes[i].ID] = nodes[i].Placement
	}

	// One query per distinct period rather than per server: with everything on the
	// default month (the common case) that is a single SUM over traffic_daily.
	sums := map[string]map[int64][2]int64{}
	for _, p := range places {
		from := trafficPeriodStart(p.TrafficPeriod, now)
		if _, done := sums[from]; done {
			continue
		}
		totals, err := m.store.NodeTrafficTotals(0, from, today)
		if err != nil {
			logErr("node traffic: cannot total", "from", from, "err", err)
			continue
		}
		sums[from] = totals
	}

	out := make(map[int64]nodeTrafficUsage, len(places))
	for id, p := range places {
		totals := sums[trafficPeriodStart(p.TrafficPeriod, now)]
		t := totals[id]
		used := t[0] + t[1]
		out[id] = nodeTrafficUsage{Used: used, Over: p.OverTrafficLimit(used)}
	}
	m.nodeTraffic.replace(out)
	m.sweepNodeTrafficAlerts(places, out)
}

// sweepNodeTrafficAlerts tells admins when a server crosses its cap, and again when
// the period rolls over and it has room. One alert per crossing, not one per sweep.
func (m *Manager) sweepNodeTrafficAlerts(places map[int64]model.Placement, usage map[int64]nodeTrafficUsage) {
	lang := m.botLang()
	type pending struct{ html string }
	var msgs []pending

	m.nodeAlertMu.Lock()
	for id, p := range places {
		u := usage[id]
		if !p.TrafficCapped() {
			// No cap: forget any alarm, so re-adding one later starts clean rather
			// than silently believing the operator was already told.
			m.nodeAlertLocked(id).trafficAlerted = false
			continue
		}
		st := m.nodeAlertLocked(id)
		switch {
		case u.Over && !st.trafficAlerted:
			st.trafficAlerted = true
			msgs = append(msgs, pending{fmt.Sprintf(i18n.T(lang, "notify.nodeTrafficOver"),
				m.serverLabel(id), humanBytes(u.Used), humanBytes(p.TrafficLimit),
				i18n.T(lang, trafficPeriodKey(p.TrafficPeriod)))})
		case !u.Over && st.trafficAlerted:
			st.trafficAlerted = false
			msgs = append(msgs, pending{fmt.Sprintf(i18n.T(lang, "notify.nodeTrafficBack"),
				m.serverLabel(id), humanBytes(u.Used), humanBytes(p.TrafficLimit))})
		}
	}
	m.nodeAlertMu.Unlock()

	// Sent outside the lock: a slow Telegram send must not hold up the next sweep.
	for _, msg := range msgs {
		m.notifyAdminEvent(model.AdminEventNodeTraffic, msg.html)
	}
}

func trafficPeriodKey(period string) string {
	if model.TrafficPeriodOr(period) == model.TrafficDay {
		return "notify.trafficPeriodDay"
	}
	return "notify.trafficPeriodMonth"
}

// serverLabel names a server for an alert: the master by its configured label, a node
// by its name. Falls back to the id, which is still enough to act on.
func (m *Manager) serverLabel(id int64) string {
	if id == model.LocalNodeID {
		if set, err := m.store.GetSettings(); err == nil && set.MasterLabel != "" {
			return set.MasterLabel
		}
		return model.LocalNodeName
	}
	if n, err := m.store.GetNode(id); err == nil && n != nil {
		return n.Name
	}
	return fmt.Sprintf("#%d", id)
}
