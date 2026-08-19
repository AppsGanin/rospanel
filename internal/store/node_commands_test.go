package store

import (
	"path/filepath"
	"testing"
	"time"
)

func cmdStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "cmd.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// An operator pressing Update again on ONE node means "it did not land, send it again",
// and it is the only evidence available that it did not: handover is not proof of
// delivery, because a lost sync response looks exactly like a node that received one.
// Swallowing that retry leaves no way to ask twice.
func TestReAskingOneNodeReArmsIt(t *testing.T) {
	st := cmdStore(t)
	now := time.Now().Unix()
	if err := st.SetNodeCommand(2, "update", now); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := st.MarkNodeCommandSent(2, "update"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if err := st.SetNodeCommand(2, "update", now+5); err != nil {
		t.Fatalf("re-ask: %v", err)
	}
	c, err := st.NodeCommand(2, "update")
	if err != nil || c == nil {
		t.Fatalf("read back: %v", err)
	}
	if c.Sent {
		t.Error("a deliberate re-ask on one node was swallowed — the operator cannot retry")
	}
	if c.At != now+5 {
		t.Errorf("the deadline was not extended: at=%d, want %d", c.At, now+5)
	}
}

// The FLEET-wide call is the opposite, and that asymmetry is the point: re-recording for
// every eligible node must not resend to the ones already handed the command, or "panel
// self-updated, now update the fleet" tells nodes that just updated to update again.
func TestReAskingTheFleetDoesNotReArmADeliveredCommand(t *testing.T) {
	st := cmdStore(t)
	now := time.Now().Unix()

	if _, err := st.SetNodeCommands([]int64{2}, "update", now); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := st.MarkNodeCommandSent(2, "update"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	// update-all runs again while the first one is still in flight.
	if _, err := st.SetNodeCommands([]int64{2}, "update", now+5); err != nil {
		t.Fatalf("re-record: %v", err)
	}

	c, err := st.NodeCommand(2, "update")
	if err != nil || c == nil {
		t.Fatalf("read back: %v", err)
	}
	if !c.Sent {
		t.Error("a fleet-wide re-record cleared `sent` — nodes that already updated will update again")
	}
	if c.At != now+5 {
		t.Errorf("the deadline was not extended: at=%d, want %d", c.At, now+5)
	}
}

// The sweep is the only thing that clears rows for a node that never comes back, and it
// had no coverage at all: inverting its predicate (deleting every LIVE command and
// keeping every stale one) left the whole suite green.
func TestPurgeNodeCommandsDropsOnlyTheStaleOnes(t *testing.T) {
	st := cmdStore(t)
	now := time.Now().Unix()

	if err := st.SetNodeCommand(1, "update", now-3600); err != nil { // stale
		t.Fatalf("record stale: %v", err)
	}
	if err := st.SetNodeCommand(2, "geo", now); err != nil { // fresh
		t.Fatalf("record fresh: %v", err)
	}

	n, err := st.PurgeNodeCommands(now - 900)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d rows, want 1", n)
	}
	if c, _ := st.NodeCommand(1, "update"); c != nil {
		t.Error("the stale command survived the sweep")
	}
	if c, _ := st.NodeCommand(2, "geo"); c == nil {
		t.Error("the sweep took a live command with it")
	}
}

// The fleet-wide write is one transaction: a per-node statement under the manager's lock
// stalled every node's sync for the duration. All-or-nothing is also what makes the
// returned count a receipt rather than an estimate.
func TestSetNodeCommandsWritesTheWholeFleetAtOnce(t *testing.T) {
	st := cmdStore(t)
	now := time.Now().Unix()

	n, err := st.SetNodeCommands([]int64{1, 2, 3}, "update", now)
	if err != nil || n != 3 {
		t.Fatalf("wrote %d rows (%v), want 3", n, err)
	}
	for _, id := range []int64{1, 2, 3} {
		if c, _ := st.NodeCommand(id, "update"); c == nil {
			t.Errorf("node %d got no command", id)
		}
	}

	// Re-asking the fleet must not re-arm the ones already handed over (see
	// TestReAskingDoesNotReArmADeliveredCommand — the same rule, in bulk).
	if err := st.MarkNodeCommandSent(2, "update"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if _, err := st.SetNodeCommands([]int64{1, 2, 3}, "update", now+10); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	if c, _ := st.NodeCommand(2, "update"); c == nil || !c.Sent {
		t.Error("a fleet-wide re-ask re-armed a node that had already been told")
	}

	if n, err := st.SetNodeCommands(nil, "update", now); err != nil || n != 0 {
		t.Errorf("empty fleet wrote %d rows (%v), want 0", n, err)
	}
}
