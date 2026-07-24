package core

import (
	"fmt"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Admin alerts about remote nodes.
//
// A node runs the same Xray and gets its own TLS certificate, but it has no
// Telegram bot of its own — the panel is the only process that can reach the
// operator. So the two admin-event categories that already cover the master's Xray
// and certificate ("Сбой Xray", "Сертификат TLS") are raised here for every node
// too, out of what each node reports on sync.
//
// Every decision is made in one periodic sweep rather than on the sync path, on
// purpose: the most common way a node stops serving — the box is down, the agent
// died, the network went — shows up as the ABSENCE of syncs, which no sync handler
// can observe. Reading the stored rows on a timer sees that failure and a reported
// one the same way, and keeps notification logic off the sync hot path.
const (
	// nodeWatchInterval is how often node state is checked for transitions. Half the
	// online window, so an unreachable node is reported about a minute after it
	// crosses it rather than a whole window later.
	nodeWatchInterval = 60 * time.Second
	// nodeXrayNotifyThrottle mirrors crashNotifyThrottle for nodes: a node whose Xray
	// is crash-looping alerts at most this often.
	nodeXrayNotifyThrottle = 5 * time.Minute
	// nodeCertErrMax truncates the error text a node reports before it goes into a
	// chat message. It is remote input, and an ACME failure can carry a whole
	// paragraph of server response.
	nodeCertErrMax = 200
)

// nodeAlertState is what admins were last told about one node, plus enough of its
// last-seen state to spot a transition. In memory only: a panel restart re-baselines
// (see the `known` flag) instead of replaying an outage that began before it, which
// is how notifyStatusTransitions treats a restart too.
type nodeAlertState struct {
	known    bool // false until the first observation ⇒ next sweep only baselines
	online   bool
	xrayUp   bool
	certSHA  string
	certSelf bool

	// offlineAlerted records that admins were actually told this node is
	// unreachable, so the all-clear is only sent for an alarm they saw.
	offlineAlerted bool
	offlineSince   int64 // node's last_seen when it went silent (for the downtime line)

	xrayAlerted    bool
	xrayDownAt     time.Time
	lastXrayNotify time.Time

	// certErr is the last TLS error this node reported (empty ⇒ its cert is fine),
	// recorded on sync and acted on by the sweep.
	certErr       string
	lastCertErrAt time.Time
}

// nodeAlertMsg is one pending message: which admin-event category gates it and the
// text. Collected under the lock and sent after it, so a slow Telegram send can't
// block the sweep's next node.
type nodeAlertMsg struct {
	bit  int64
	html string
}

// nodeWatchLoop drives the node alert sweep. The first pass only records a
// baseline, so a panel that starts up next to a long-dead node stays quiet.
func (m *Manager) nodeWatchLoop() {
	t := time.NewTicker(nodeWatchInterval)
	defer t.Stop()
	for {
		m.SweepNodeAlerts()
		<-t.C
	}
}

// SweepNodeAlerts compares every node's current state against what admins were last
// told and sends the differences.
func (m *Manager) SweepNodeAlerts() {
	nodes, err := m.store.ListNodes()
	if err != nil {
		logErr("node alerts: cannot list nodes", "err", err)
		return
	}
	now := time.Now()
	live := make(map[int64]struct{}, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || !n.Joined() {
			// Switched off on purpose, or never installed on a server: neither is an
			// outage. Forget the state so re-enabling starts from a fresh baseline
			// rather than announcing an "outage" that was the operator's own doing.
			m.forgetNodeAlerts(n.ID)
			continue
		}
		live[n.ID] = struct{}{}
		for _, msg := range m.nodeAlertsFor(n, now) {
			m.notifyAdminEvent(msg.bit, msg.html)
		}
	}
	m.pruneNodeAlerts(live)
}

// nodeAlertsFor advances one node's alert state and returns the messages that
// transition produced. Sending is left to the caller: the state lock is held here.
func (m *Manager) nodeAlertsFor(n *model.Node, now time.Time) []nodeAlertMsg {
	m.nodeAlertMu.Lock()
	defer m.nodeAlertMu.Unlock()
	st := m.nodeAlertLocked(n.ID)
	online := n.Online(now.Unix())

	if !st.known {
		st.known, st.online, st.xrayUp = true, online, n.XrayRunning
		st.certSHA, st.certSelf = n.CertSHA256, n.CertSelfSigned
		return nil // baseline: report changes from here on, never the starting state
	}

	var out []nodeAlertMsg
	switch {
	case st.online && !online:
		st.offlineAlerted, st.offlineSince = true, n.LastSeen
		out = append(out, nodeAlertMsg{model.AdminEventXrayDown, fmt.Sprintf(
			"⚠️ <b>Нет связи с сервером</b>\n%s\nНе отвечает %s — его пользователи сейчас не обслуживаются.",
			nodeLabel(n), fmtDowntime(now.Sub(time.Unix(n.LastSeen, 0))))})
	case !st.online && online && st.offlineAlerted:
		st.offlineAlerted = false
		msg := "✅ <b>Связь с сервером восстановлена</b>\n" + nodeLabel(n)
		if st.offlineSince > 0 {
			msg += fmt.Sprintf("\nПростой: %s.", fmtDowntime(now.Sub(time.Unix(st.offlineSince, 0))))
		}
		out = append(out, nodeAlertMsg{model.AdminEventXrayDown, msg})
	}
	st.online = online

	// Everything below reads what the node reported. While it is silent that report
	// is stale — its Xray may well be down with the box — so it is not evaluated:
	// the unreachable alert above is the one that fits, and a second alarm from
	// frozen data would only muddy it.
	if !online {
		return out
	}

	switch {
	case st.xrayUp && !n.XrayRunning:
		// Throttled like the master's own crash alert, so a crash-looping node reports
		// at a sane rate. A throttled-away alarm leaves xrayAlerted alone, so no
		// all-clear is sent for an outage nobody was told about.
		if now.Sub(st.lastXrayNotify) >= nodeXrayNotifyThrottle {
			st.lastXrayNotify, st.xrayAlerted, st.xrayDownAt = now, true, now
			out = append(out, nodeAlertMsg{model.AdminEventXrayDown, fmt.Sprintf(
				"⚠️ <b>Xray аварийно завершился</b>\n%s\nАгент перезапускает процесс автоматически.",
				nodeLabel(n))})
		}
	case !st.xrayUp && n.XrayRunning && st.xrayAlerted:
		st.xrayAlerted = false
		msg := "✅ <b>Xray снова работает</b>\n" + nodeLabel(n)
		if down := now.Sub(st.xrayDownAt); down > time.Second {
			msg += fmt.Sprintf("\nПростой: %s.", fmtDowntime(down))
		}
		out = append(out, nodeAlertMsg{model.AdminEventXrayDown, msg})
	}
	st.xrayUp = n.XrayRunning

	// A changed fingerprint on a CA-signed cert is a renewal that landed. Self-signed
	// is the agent's fallback while ACME is unavailable, not an event: it changes on
	// its own schedule and says nothing an operator can act on.
	if n.CertSHA256 != "" && n.CertSHA256 != st.certSHA && !n.CertSelfSigned {
		verb := "обновлён"
		if st.certSHA == "" || st.certSelf {
			verb = "выпущен" // first real cert for this node, not a renewal
		}
		msg := fmt.Sprintf("🔒 <b>Сертификат TLS %s</b>\n%s", verb, nodeLabel(n))
		if days := certDaysLeft(n.CertExpiresAt, now); days >= 0 {
			msg += fmt.Sprintf("\nДействует ещё %d дн.", days)
		}
		out = append(out, nodeAlertMsg{model.AdminEventCert, msg})
	}
	st.certSHA, st.certSelf = n.CertSHA256, n.CertSelfSigned

	if st.certErr != "" && now.Sub(st.lastCertErrAt) >= certErrNotifyThrottle {
		st.lastCertErrAt = now
		out = append(out, nodeAlertMsg{model.AdminEventCert, fmt.Sprintf(
			"🔓 <b>Не удалось обновить сертификат TLS</b>\n%s\nОшибка: %s",
			nodeLabel(n), escHTML(st.certErr))})
	}
	return out
}

// NoteNodeCertError records the TLS error a node reported on its sync (empty ⇒ its
// certificate is fine). Only the state is written here — the alert is raised by the
// sweep, which owns the throttle and the rest of the node's alert state.
func (m *Manager) NoteNodeCertError(nodeID int64, msg string) {
	if len(msg) > nodeCertErrMax {
		msg = msg[:nodeCertErrMax] + "…"
	}
	m.nodeAlertMu.Lock()
	defer m.nodeAlertMu.Unlock()
	st := m.nodeAlertLocked(nodeID)
	if msg != st.certErr {
		// A new error — or one that just cleared — starts the throttle over, so the
		// next distinct failure is reported promptly instead of waiting out the window
		// of the previous one.
		st.lastCertErrAt = time.Time{}
	}
	st.certErr = msg
}

// nodeAlertLocked returns the node's alert state, creating it on first use. The map
// is built lazily so a Manager assembled without New (tests, CLI paths) works too.
// Caller holds nodeAlertMu.
func (m *Manager) nodeAlertLocked(id int64) *nodeAlertState {
	if m.nodeAlerts == nil {
		m.nodeAlerts = map[int64]*nodeAlertState{}
	}
	st := m.nodeAlerts[id]
	if st == nil {
		st = &nodeAlertState{}
		m.nodeAlerts[id] = st
	}
	return st
}

func (m *Manager) forgetNodeAlerts(id int64) {
	m.nodeAlertMu.Lock()
	defer m.nodeAlertMu.Unlock()
	delete(m.nodeAlerts, id)
}

// pruneNodeAlerts drops state for nodes that are gone, so the map tracks the fleet
// rather than every node the panel has ever had.
func (m *Manager) pruneNodeAlerts(live map[int64]struct{}) {
	m.nodeAlertMu.Lock()
	defer m.nodeAlertMu.Unlock()
	for id := range m.nodeAlerts {
		if _, ok := live[id]; !ok {
			delete(m.nodeAlerts, id)
		}
	}
}

// nodeLabel names a node in an alert. The host rides along with the name because an
// abuse complaint, a hoster's mail and a traceroute all name the address, not the
// label the operator picked in the panel.
func nodeLabel(n *model.Node) string {
	s := "Сервер: " + escHTML(n.Name)
	if n.Host != "" {
		s += " (" + escHTML(n.Host) + ")"
	}
	return s
}

// certDaysLeft is whole days from now until expiry, or -1 when the node hasn't
// reported one (an older agent doesn't send it).
func certDaysLeft(expiresAt int64, now time.Time) int {
	if expiresAt <= 0 {
		return -1
	}
	d := time.Unix(expiresAt, 0).Sub(now)
	if d < 0 {
		return 0
	}
	return int(d.Hours() / 24)
}
