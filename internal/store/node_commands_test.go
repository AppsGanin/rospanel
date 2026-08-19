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

// Re-asking must not re-arm a command the node has already been handed. The rows outlive
// a panel restart now, and update-all re-records for every eligible node, so clearing
// `sent` here would tell nodes that had just updated to update again — fleet-wide, in
// exactly the "panel self-updated, now update the fleet" workflow.
func TestReAskingDoesNotReArmADeliveredCommand(t *testing.T) {
	st := cmdStore(t)
	now := time.Now().Unix()

	if err := st.SetNodeCommand(2, "update", now); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := st.MarkNodeCommandSent(2, "update"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	// The operator asks again while it is still in flight.
	if err := st.SetNodeCommand(2, "update", now+5); err != nil {
		t.Fatalf("re-record: %v", err)
	}

	c, err := st.NodeCommand(2, "update")
	if err != nil || c == nil {
		t.Fatalf("read back: %v", err)
	}
	if !c.Sent {
		t.Error("re-asking cleared `sent` — the node will be told to run it a second time")
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
