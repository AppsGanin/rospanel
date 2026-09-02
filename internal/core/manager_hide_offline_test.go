package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A node that has stopped reporting stays in subscriptions by default and drops out
// only when the operator asks for it — and a node that is merely disabled or has
// never installed is out either way.
func TestHideOfflineNodesFromSubscriptions(t *testing.T) {
	m := bulkTestManager(t)
	now := time.Now().Unix()
	live, err := m.CreateNode("live", "live.example.com")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := m.CreateNode("stale", "stale.example.com")
	if err != nil {
		t.Fatal(err)
	}
	// Both have installed (a cert fingerprint is what makes a node linkable); one
	// synced a second ago, the other well outside the online window.
	for id, seen := range map[int64]int64{live.ID: now, stale.ID: now - model.NodeOnlineWindow - 60} {
		if err := m.store.UpdateNodeStatus(id, model.NodeStatusUpdate{
			LastSeen: seen, NodeVersion: "test", XrayRunning: true,
			// A node with no reported fingerprint is skipped whatever its liveness
			// (its links could not be pinned), so both report one.
			CertSHA256: "aa", CertSelfSigned: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	names := func() []string {
		t.Helper()
		sets, err := m.NodeLinkSettings()
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(sets))
		for _, s := range sets {
			out = append(out, s.NodeLabel)
		}
		return out
	}

	if got := names(); len(got) != 2 {
		t.Fatalf("by default both nodes stay in the subscription, got %v", got)
	}

	set, _ := m.store.GetSettings()
	set.SubHideOffline = true
	if err := m.SaveSubSettings(set); err != nil {
		t.Fatal(err)
	}
	got := names()
	if len(got) != 1 || got[0] != "live" {
		t.Fatalf("with the switch on only the live node stays, got %v", got)
	}

	// The stale node comes back on its next sync, without an operator touching
	// anything — the list follows what the node reports.
	if err := m.store.UpdateNodeStatus(stale.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), NodeVersion: "test", XrayRunning: true,
		CertSHA256: "aa", CertSelfSigned: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := names(); len(got) != 2 {
		t.Errorf("a node that reported again should be back, got %v", got)
	}

	// Disabled stays out regardless of the switch.
	if err := m.store.SetNodeEnabled(live.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := names(); len(got) != 1 || got[0] != "stale" {
		t.Errorf("a disabled node must never be linked, got %v", got)
	}
}
