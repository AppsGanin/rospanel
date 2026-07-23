package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/store"
)

// restartTestManager returns a manager with a node registered, ready to take
// restart requests.
func restartTestManager(t *testing.T) (*Manager, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "restart.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	n, err := st.CreateNode("edge", "203.0.113.9", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	m := &Manager{
		store:       st,
		nodes:       newNodeRegistry(),
		nodeRestart: map[int64]*nodeRestartReq{},
	}
	return m, n.ID
}

// The restart button used to report success the moment it was clicked, which is how
// a node with a dead supervisor could swallow three restarts in a row and still look
// fine. A request must therefore survive being SENT and only clear when the node
// reports an Xray that actually came back.
func TestNodeRestartConfirmsOnlyOnAFreshXray(t *testing.T) {
	m, id := restartTestManager(t)

	if got := m.NodeRestartState(id); got != "" {
		t.Fatalf("a node nobody touched reads as %q", got)
	}
	if err := m.RequestNodeXrayRestart(id); err != nil {
		t.Fatalf("request: %v", err)
	}
	if got := m.NodeRestartState(id); got != RestartPending {
		t.Fatalf("right after the operator asked: %q, want %q", got, RestartPending)
	}

	// The node's poll returns and carries the command away. Xray was up since 1000.
	if !m.TakeNodeXrayRestart(id, 1000) {
		t.Fatal("the command was not handed to the node")
	}
	// Sending is not doing: the operator is still waiting to hear it happened.
	if got := m.NodeRestartState(id); got != RestartPending {
		t.Errorf("state on delivery = %q — reporting done when it was merely sent is "+
			"the lie this exists to stop", got)
	}
	// And it is handed over exactly once: re-sending on every poll would restart the
	// node again and again while we wait.
	if m.TakeNodeXrayRestart(id, 1000) {
		t.Error("the command was handed over twice")
	}

	// A sync that still shows the SAME Xray proves nothing — it is the report that
	// was already in flight, or a node that never acted.
	m.ConfirmNodeXrayRestart(id, 1000)
	if got := m.NodeRestartState(id); got != RestartPending {
		t.Errorf("state = %q — an unchanged Xray start time was taken as proof", got)
	}

	// A different start time is the node's own proof that the process bounced.
	m.ConfirmNodeXrayRestart(id, 1042)
	if got := m.NodeRestartState(id); got != RestartDone {
		t.Errorf("state after the node proved the bounce = %q, want %q", got, RestartDone)
	}

	// The answer is held briefly and then stops being news: confirmation lands about
	// a second after the click, so without this window the operator sees nothing
	// change at all and clicks again.
	m.nodeUpdateMu.Lock()
	m.nodeRestart[id].outcomeAt = time.Now().Add(-nodeRestartShow - time.Second)
	m.nodeUpdateMu.Unlock()
	if got := m.NodeRestartState(id); got != "" {
		t.Errorf("state = %q long after it resolved, want it gone", got)
	}
}

// An agent too old to report its Xray start time can never prove anything, so its
// request must not hang the button forever — nor be mistaken for a success.
func TestNodeRestartGivesUpWaiting(t *testing.T) {
	m, id := restartTestManager(t)

	if err := m.RequestNodeXrayRestart(id); err != nil {
		t.Fatalf("request: %v", err)
	}
	if !m.TakeNodeXrayRestart(id, 0) {
		t.Fatal("the command was not handed to the node")
	}
	// A node reporting no start time at all (old agent) never confirms.
	m.ConfirmNodeXrayRestart(id, 0)
	if got := m.NodeRestartState(id); got != RestartPending {
		t.Errorf("state = %q — a zero start time was accepted as proof", got)
	}

	// Age the request past the wait: the UI must fall back to the server's real
	// status rather than keep claiming a restart is on its way.
	m.nodeUpdateMu.Lock()
	m.nodeRestart[id].at = time.Now().Add(-nodeRestartWait - time.Second)
	m.nodeUpdateMu.Unlock()

	if got := m.NodeRestartState(id); got != RestartTimeout {
		t.Errorf("state after the wait = %q, want %q — giving up has to be said out "+
			"loud, not shown as the badge quietly vanishing", got, RestartTimeout)
	}
	// Expiry also stops the command from being delivered late — a node that was
	// offline must not bounce Xray minutes after the operator gave up.
	if err := m.RequestNodeXrayRestart(id); err != nil {
		t.Fatalf("request: %v", err)
	}
	m.nodeUpdateMu.Lock()
	m.nodeRestart[id].at = time.Now().Add(-nodeRestartWait - time.Second)
	m.nodeUpdateMu.Unlock()
	if m.TakeNodeXrayRestart(id, 500) {
		t.Error("an expired request was still handed to the node")
	}
}
