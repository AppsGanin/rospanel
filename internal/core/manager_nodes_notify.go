package core

import (
	"fmt"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sysstat"
)

// Admin alerts about remote nodes.
//
// A node runs the same Xray and gets its own TLS certificate, but it has no
// Telegram bot of its own — the panel is the only process that can reach the
// operator. So the two admin-event categories that already cover the master's Xray
// and certificate ("Xray failure", "TLS certificate") are raised here for every node
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

	// diskLowAlerted records that admins were told this server is running out of
	// space, so the all-clear is only sent for an alarm they saw.
	diskLowAlerted bool

	// trafficAlerted records that admins were told this server reached its traffic
	// cap, so they are told once per crossing rather than once per sweep — and get an
	// all-clear only for an alarm they actually saw.
	trafficAlerted bool

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
		// Per-server traffic caps ride the same tick: the question is the same shape
		// (compare a server against a threshold, tell admins once per crossing) and the
		// answer is a SUM the subscription path must not be paying for per request.
		m.refreshNodeTraffic()
		<-t.C
		// The status page's history rides this tick: it needs the same "is each server
		// up" question the sweep just answered, on the same cadence, and a second timer
		// asking it again would only add writes.
		//
		// After the wait, not before it: at boot Xray has not started yet, so an
		// immediate sample records an outage on every panel restart — the status page
		// would show a red bar for the operator updating it.
		m.SampleUptime()
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
	// The master's own figures are read here rather than deep inside: Sampler reports
	// the real host and cannot be pointed at made-up numbers, so keeping it at the
	// edge leaves the sweep itself something a test can drive.
	var local *sysstat.Stats
	if m.sys != nil {
		st := m.sys.Read()
		local = &st
	}
	m.sweepAlerts(nodes, local, time.Now())
}

func (m *Manager) sweepAlerts(nodes []model.Node, local *sysstat.Stats, now time.Time) {
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
		var diskUsed, diskTotal int64
		if h, ok := m.NodeHostStats(n.ID); ok {
			diskUsed, diskTotal = h.DiskUsed, h.DiskTotal
		}
		for _, msg := range m.nodeAlertsFor(n, now, diskUsed, diskTotal) {
			m.notifyAdminEvent(msg.bit, msg.html)
		}
	}
	if local != nil {
		if msg := m.localDiskAlertMsg(live, local.DiskUsed, local.DiskTotal); msg != "" {
			m.notifyAdminEvent(model.AdminEventXrayDown, msg)
		}
	}
	m.pruneNodeAlerts(live)
}

// sweepLocalDiskAlert runs the same free-space check over the panel's own machine.
// It is separate because the master is not in ListNodes — it is a virtual node the API
// view assembles — so the sweep above never sees it, and the machine the panel itself
// runs on is the one whose full disk breaks everything at once.

// localDiskAlertMsg advances the master's own disk state and returns what to say, or ""
// for nothing. Split from the sending half — the same shape nodeAlertsFor has — so the
// step between "the figures crossed a threshold" and "an admin was told" is one a test
// can walk, rather than the kind of wiring that is only discovered missing in
// production.
func (m *Manager) localDiskAlertMsg(live map[int64]struct{}, used, total int64) string {
	live[model.LocalNodeID] = struct{}{} // keep the state from being pruned as stale
	m.nodeAlertMu.Lock()
	defer m.nodeAlertMu.Unlock()
	st := m.nodeAlertLocked(model.LocalNodeID)
	next, msg := diskAlert(st.diskLowAlerted, used, total, model.LocalNodeName, m.botLang())
	st.diskLowAlerted = next
	st.known = true
	return msg
}

// nodeAlertsFor advances one node's alert state and returns the messages that
// transition produced. Sending is left to the caller: the state lock is held here.
// diskUsed/diskTotal are read by the caller rather than looked up here: they live
// behind a different lock, and taking it while holding the alert lock would introduce
// an ordering between two mutexes that currently have none.
func (m *Manager) nodeAlertsFor(n *model.Node, now time.Time, diskUsed, diskTotal int64) []nodeAlertMsg {
	m.nodeAlertMu.Lock()
	defer m.nodeAlertMu.Unlock()
	st := m.nodeAlertLocked(n.ID)
	online := n.Online(now.Unix())
	lang := m.botLang()

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
			i18n.T(lang, "notify.nodeOffline"),
			nodeLabel(n), fmtDowntime(now.Sub(time.Unix(n.LastSeen, 0)), lang))})
	case !st.online && online && st.offlineAlerted:
		st.offlineAlerted = false
		msg := i18n.T(lang, "notify.nodeBack") + "\n" + nodeLabel(n)
		if st.offlineSince > 0 {
			msg += "\n" + i18n.T(lang, "notify.downtime", fmtDowntime(now.Sub(time.Unix(st.offlineSince, 0)), lang))
		}
		out = append(out, nodeAlertMsg{model.AdminEventXrayDown, msg})
	}
	st.online = online

	// Free space, from the figures the node already reports for the panel's own
	// dashboard. Nothing else watches this: the disk filling up stops SQLite writing,
	// which shows up as traffic that is not recorded and users that do not sync —
	// symptoms an operator has no reason to connect to a full disk, on a machine they
	// have no reason to be logged into.
	if next, msg := diskAlert(st.diskLowAlerted, diskUsed, diskTotal, nodeLabel(n), lang); msg != "" {
		st.diskLowAlerted = next
		out = append(out, nodeAlertMsg{model.AdminEventXrayDown, msg})
	}

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
				i18n.T(lang, "notify.nodeXrayCrashed"),
				nodeLabel(n))})
		}
	case !st.xrayUp && n.XrayRunning && st.xrayAlerted:
		st.xrayAlerted = false
		msg := i18n.T(lang, "notify.nodeXrayBack") + "\n" + nodeLabel(n)
		if down := now.Sub(st.xrayDownAt); down > time.Second {
			msg += "\n" + i18n.T(lang, "notify.downtime", fmtDowntime(down, lang))
		}
		out = append(out, nodeAlertMsg{model.AdminEventXrayDown, msg})
	}
	st.xrayUp = n.XrayRunning

	// A changed fingerprint on a CA-signed cert is a renewal that landed. Self-signed
	// is the agent's fallback while ACME is unavailable, not an event: it changes on
	// its own schedule and says nothing an operator can act on.
	if n.CertSHA256 != "" && n.CertSHA256 != st.certSHA && !n.CertSelfSigned {
		key := "notify.nodeCertRenewed"
		if st.certSHA == "" || st.certSelf {
			key = "notify.nodeCertIssued" // first real cert for this node, not a renewal
		}
		msg := i18n.T(lang, key, nodeLabel(n))
		if days := certDaysLeft(n.CertExpiresAt, now); days >= 0 {
			msg += "\n" + i18n.TN(lang, "notify.validForDays", days)
		}
		out = append(out, nodeAlertMsg{model.AdminEventCert, msg})
	}
	st.certSHA, st.certSelf = n.CertSHA256, n.CertSelfSigned

	if st.certErr != "" && now.Sub(st.lastCertErrAt) >= certErrNotifyThrottle {
		st.lastCertErrAt = now
		out = append(out, nodeAlertMsg{model.AdminEventCert, fmt.Sprintf(
			i18n.T(lang, "notify.nodeCertFailed"),
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
		// The master is always live — it is the panel — and it is never in the node
		// list the sweep builds `live` from. Pruning it would drop every flag recording
		// what admins were already told about this server, so the next sweep would tell
		// them again: an alarm every minute for a condition that has not changed. It
		// used to be saved by localDiskAlertMsg inserting itself into `live`, which
		// only runs when the host sampler exists; the rule belongs here instead.
		if id == model.LocalNodeID {
			continue
		}
		if _, ok := live[id]; !ok {
			delete(m.nodeAlerts, id)
		}
	}
}

// nodeLabel names a node in an alert. The host rides along with the name because an
// abuse complaint, a hoster's mail and a traceroute all name the address, not the
// label the operator picked in the panel.
func nodeLabel(n *model.Node) string {
	s := i18n.T(i18n.Default, "notify.serverIs", escHTML(n.Name))
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

// Disk thresholds. Alerting starts at the same 15% the health report already calls a
// warning, so the panel does not grade one way and shout another. The all-clear waits
// for 20% rather than 15 on purpose: a disk sitting exactly on the line would
// otherwise alternate between alarm and all-clear on every sweep, and an alert that
// cries wolf is one an operator learns to ignore.
const (
	diskAlertFreePct = 15
	diskClearFreePct = 20
)

// diskAlert decides what to tell admins about free space, given what they were last
// told. Returns the new alerted state and a message, or "" for nothing to say.
//
// A server that reports no disk figures at all (an older node, or one whose stats have
// not arrived yet) says nothing rather than reading as a full disk.
func diskAlert(alerted bool, used, total int64, label string, lang i18n.Lang) (bool, string) {
	if total <= 0 {
		return alerted, ""
	}
	freePct := int(float64(total-used) / float64(total) * 100)
	switch {
	case !alerted && freePct < diskAlertFreePct:
		return true, fmt.Sprintf(i18n.T(lang, "notify.diskLow"), label, freePct,
			humanBytes(total-used), humanBytes(total))
	case alerted && freePct >= diskClearFreePct:
		return false, fmt.Sprintf(i18n.T(lang, "notify.diskBack"), label, freePct)
	}
	return alerted, ""
}
