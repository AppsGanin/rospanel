package core

import (
	"time"

	"github.com/AppsGanin/rospanel/internal/logbuf"
	"github.com/AppsGanin/rospanel/internal/model"
)

// RequestNodeUpdate flags a node to self-update on its next sync, and wakes it so
// it happens promptly. Returns an error if the node doesn't exist.
func (m *Manager) RequestNodeUpdate(id int64) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	m.nodeUpdateMu.Lock()
	err = m.store.SetNodeCommand(id, nodeCmdUpdate, time.Now().Unix())
	m.nodeUpdateMu.Unlock()
	if err != nil {
		// Report it: the whole point of putting the command on disk is that the
		// operator is told whether it was actually recorded.
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// takeCmd is the shared handover: deliver once, then keep the request until the node
// returns. Reports whether the command should ride this response.
//
// Backed by the store rather than a map so a panel restart does not drop what an
// operator asked for — and the restart that used to drop it is most often the panel's
// own self-update, i.e. exactly when the fleet is asked to update too.
func (m *Manager) takeCmd(id int64, kind string) bool {
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	c, err := m.store.NodeCommand(id, kind)
	if err != nil || c == nil {
		if err != nil {
			logErr("node command: read failed", "node", id, "kind", kind, "err", err)
		}
		return false
	}
	if time.Since(time.Unix(c.At, 0)) >= nodeCmdTTL {
		// Aged out. A node that was offline must not act on an order the operator gave
		// up on minutes ago.
		_ = m.store.DeleteNodeCommand(id, kind)
		return false
	}
	if c.Sent {
		// The node is back after being told — the response landed. Done.
		_ = m.store.DeleteNodeCommand(id, kind)
		return false
	}
	if err := m.store.MarkNodeCommandSent(id, kind); err != nil {
		logErr("node command: marking sent failed", "node", id, "kind", kind, "err", err)
		return false // don't send what we cannot record, or it is delivered forever
	}
	return true
}

// nodeRestartReq is one operator-requested Xray restart, tracked from the click
// until the node proves it happened — and then a little longer, so the answer is
// seen.
type nodeRestartReq struct {
	at   time.Time // when the operator asked (drives the timeout)
	sent bool      // handed to the node in a sync response
	// priorStart is the node's reported Xray start time captured at the moment the
	// command was handed over. Confirmation is "this value changed", never a compare
	// against the panel's own clock: the two machines' clocks disagree freely (this
	// pair runs an hour apart), and a node whose clock trails the panel's would never
	// look restarted at all.
	priorStart int64
	// outcome is RestartDone/RestartTimeout once resolved, "" while still waiting;
	// outcomeAt starts the window it stays on screen for.
	outcome   string
	outcomeAt time.Time
}

// waiting reports whether this request is still owed an answer — not yet resolved,
// and not yet out of time.
func (r *nodeRestartReq) waiting(now time.Time) bool {
	return r != nil && r.outcome == "" && now.Sub(r.at) < nodeRestartWait
}

// RequestNodeXrayRestart flags a node to bounce its Xray on the next sync and wakes
// it so it happens promptly. The panel can't reach into a node's process — this is
// the node-side twin of the master's own Xray restart button.
//
// The request stays on record after it is sent, until the node's next sync proves
// Xray actually bounced (see ConfirmNodeXrayRestart). That is the whole point: the
// panel cannot restart a node, only ask, and an answer that always reads "done"
// the instant it is asked is not an answer.
func (m *Manager) RequestNodeXrayRestart(id int64) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	m.nodeUpdateMu.Lock()
	m.nodeRestart[id] = &nodeRestartReq{at: time.Now()}
	m.nodeUpdateMu.Unlock()
	m.nodes.wakeOne(id)
	return nil
}

// TakeNodeXrayRestart reports whether this node should be told to bounce Xray now,
// and marks the command as sent. reportedStart is the Xray start time the node just
// reported, remembered as the "before" the confirmation compares against.
//
// It returns true exactly once per request: the node must not restart again on
// every poll while the panel is still waiting to hear that the first one landed.
func (m *Manager) TakeNodeXrayRestart(id int64, reportedStart int64) bool {
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	r := m.nodeRestart[id]
	if !r.waiting(time.Now()) || r.sent {
		// Absent, already answered, already sent, or out of time. The last case is
		// deliberate: a node that was offline must not bounce Xray minutes after the
		// operator gave up on the request.
		return false
	}
	r.sent = true
	r.priorStart = reportedStart
	return true
}

// ConfirmNodeXrayRestart clears a pending restart once the node reports an Xray that
// started at a different time than the one running when the command went out — the
// node's own proof that the process really came back.
//
// A node that reports no start time at all (an agent older than this field) can
// never confirm; its request simply times out, which reads as "we asked, we can't
// tell" rather than a false "done".
func (m *Manager) ConfirmNodeXrayRestart(id int64, reportedStart int64) {
	if reportedStart == 0 {
		return
	}
	now := time.Now()
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	r := m.nodeRestart[id]
	if r.waiting(now) && r.sent && reportedStart != r.priorStart {
		r.outcome, r.outcomeAt = RestartDone, now
	}
}

// NodeRestartState is what this node's restart request currently reads as:
// RestartPending while waiting, then RestartDone or RestartTimeout for a short
// window, then "" (nothing to say). Records past their window are dropped here, so
// the state can never be left stuck on a stale answer.
//
// The timeout is decided lazily, on read, rather than by a timer: nothing else in
// the panel needs to know, and a request nobody is looking at costs nothing to let
// sit until someone asks.
func (m *Manager) NodeRestartState(id int64) string {
	now := time.Now()
	m.nodeUpdateMu.Lock()
	defer m.nodeUpdateMu.Unlock()
	r := m.nodeRestart[id]
	switch {
	case r == nil:
		return ""
	case r.outcome != "":
		if now.Sub(r.outcomeAt) < nodeRestartShow {
			return r.outcome
		}
		delete(m.nodeRestart, id)
		return ""
	case r.waiting(now):
		return RestartPending
	default:
		// Out of time with no proof. Say so — and keep saying it for the display
		// window, so "we asked and never heard back" is an answer the operator
		// actually sees rather than the badge just disappearing.
		r.outcome, r.outcomeAt = RestartTimeout, now
		return RestartTimeout
	}
}

// RequestAllNodesUpdate flags every enabled, connected node to self-update.
func (m *Manager) RequestAllNodesUpdate() (int, error) {
	nodes, err := m.store.ListNodes()
	if err != nil {
		return 0, err
	}
	ids := make([]int64, 0, len(nodes))
	for i := range nodes {
		if nodes[i].Enabled && nodes[i].LastSeen > 0 {
			ids = append(ids, nodes[i].ID)
		}
	}
	// One transaction, so the lock is held for a single round trip rather than one per
	// node: every node's sync and the panel's Nodes page queue behind it otherwise. The
	// count is what actually landed — the operator's only receipt — so a failed write
	// reports zero rather than a number nobody honoured.
	m.nodeUpdateMu.Lock()
	n, err := m.store.SetNodeCommands(ids, nodeCmdUpdate, time.Now().Unix())
	m.nodeUpdateMu.Unlock()
	if err != nil {
		return 0, err
	}
	m.notifyNodes()
	return n, nil
}

// RequestNodeLogs returns a server's recent log tail.
//
// For the panel's own machine that is simply the in-memory ring every log line goes
// through. For a node it is a request: the panel marks that someone is watching,
// wakes the node's held poll, and the tail arrives on the sync that follows.
//
// It waits briefly for that to happen rather than answering empty and expecting the
// caller to ask again. The panel's own log viewer polls, so it never noticed; every
// other caller — an integration, an assistant asking once "why did this node restart"
// — got `{"lines":[],"at":0}` and no hint that asking twice was the protocol.
func (m *Manager) RequestNodeLogs(id int64) ([]string, int64) {
	if id == model.LocalNodeID {
		// The master never syncs with itself; its logs are right here.
		return logbuf.Default.Tail(), time.Now().Unix()
	}

	m.nodeLogsMu.Lock()
	m.nodeLogsWanted[id] = time.Now().Unix()
	e := m.nodeLogs[id]
	m.nodeLogsMu.Unlock()
	m.nodes.wakeOne(id) // return the held poll promptly so the tail comes back fast

	deadline := time.Now().Add(nodeLogsWait)
	for {
		m.nodeLogsMu.Lock()
		fresh := m.nodeLogs[id]
		m.nodeLogsMu.Unlock()
		// Newer than what we started with ⇒ the woken node answered.
		if fresh.at > e.at {
			return fresh.lines, fresh.at
		}
		if time.Now().After(deadline) {
			// Whatever was already stored: a node that is offline or slow still gets
			// its last known tail rendered, which beats an empty box.
			return e.lines, e.at
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// WantNodeLogs reports (and is used by the sync handler to set WantLogs) whether an
// operator is currently viewing this node's logs.
func (m *Manager) WantNodeLogs(id int64) bool {
	m.nodeLogsMu.Lock()
	defer m.nodeLogsMu.Unlock()
	last, ok := m.nodeLogsWanted[id]
	return ok && time.Now().Unix()-last < int64(nodeLogsWantWindow/time.Second)
}

// storeNodeLogs records a node's reported log tail.
func (m *Manager) storeNodeLogs(id int64, lines []string) {
	if len(lines) == 0 {
		return
	}
	m.nodeLogsMu.Lock()
	m.nodeLogs[id] = nodeLogEntry{lines: lines, at: time.Now().Unix()}
	m.nodeLogsMu.Unlock()
}

// TakeNodeUpdate hands over a node's pending self-update, if it has one. The lock lives
// in takeCmd — taking it here too self-deadlocks, since Go mutexes do not re-enter.
func (m *Manager) TakeNodeUpdate(id int64) bool {
	return m.takeCmd(id, nodeCmdUpdate)
}

// RequestNodeGeoRefresh flags a node to re-download its geo databases on its next
// sync and wakes it so it happens promptly.
func (m *Manager) RequestNodeGeoRefresh(id int64) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	m.nodeUpdateMu.Lock()
	err = m.store.SetNodeCommand(id, nodeCmdGeo, time.Now().Unix())
	m.nodeUpdateMu.Unlock()
	if err != nil {
		// Report it: the whole point of putting the command on disk is that the
		// operator is told whether it was actually recorded.
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// SetNodeGeoRefresh sets a node's own geo auto-refresh cadence (hours; 0 ⇒ never) and
// wakes it so the new cadence reaches its agent (via NodeMeta) promptly.
func (m *Manager) SetNodeGeoRefresh(id int64, hours int) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	if err := m.store.SetNodeGeoRefresh(id, hours); err != nil {
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// TakeNodeGeoRefresh consumes (and clears) a node's pending geo-refresh flag.
func (m *Manager) TakeNodeGeoRefresh(id int64) bool {
	return m.takeCmd(id, nodeCmdGeo)
}
