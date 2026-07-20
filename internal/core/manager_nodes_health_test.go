package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// syncedNode registers a node and marks it as having just checked in on the given
// Xray version, the way a real sync does.
func syncedNode(t *testing.T, st *store.Store, name, xrayVersion string) {
	t.Helper()
	n, err := st.CreateNode(name, "203.0.113.1", "")
	if err != nil {
		t.Fatalf("create node %s: %v", name, err)
	}
	if err := st.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen:    time.Now().Unix(),
		NodeVersion: "1.1.0",
		XrayVersion: xrayVersion,
		XrayRunning: true,
	}); err != nil {
		t.Fatalf("status %s: %v", name, err)
	}
}

// TestNodeOnPinnedXrayIsNotStale pins a bug operators actually hit: a node running
// exactly the pinned Xray was reported as outdated forever, so the dashboard kept
// asking them to update a node they had just updated.
//
// The cause is a format difference, not a version difference. PinnedVersion carries
// a leading "v"; `xray version` does not, so a raw == never matches. The Nodes tab
// went through VersionMatchesPinned and considered the same node fine, which is why
// the two screens disagreed.
func TestNodeOnPinnedXrayIsNotStale(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	// Exactly what a node reports after updating: the pinned release, no "v".
	syncedNode(t, st, "edge-1", strings.TrimPrefix(xray.PinnedVersion, "v"))

	h := m.nodesHealth()
	if h == nil {
		t.Fatal("no node health check produced")
	}
	if h.Status != healthOK {
		t.Fatalf("a node on the pinned Xray reads as %q (%q) — operators are told to "+
			"update a node that is already up to date", h.Status, h.Detail)
	}
	if strings.Contains(h.Detail, "устаревш") {
		t.Errorf("detail still claims a stale version: %q", h.Detail)
	}
}

// TestNodeOnOldXrayIsStale guards the other side: the check must still notice a
// node that genuinely lags.
func TestNodeOnOldXrayIsStale(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "health-old.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	syncedNode(t, st, "edge-old", "1.8.0")

	h := m.nodesHealth()
	if h == nil {
		t.Fatal("no node health check produced")
	}
	if h.Status != healthWarn || !strings.Contains(h.Detail, "устаревш") {
		t.Fatalf("a node on Xray 1.8.0 reads as %q (%q), want a stale-version warning",
			h.Status, h.Detail)
	}
}

// TestNodeVersionPrefixIsToleratedBothWays: whichever side grows or loses the "v",
// the same release must never count as stale.
func TestNodeVersionPrefixIsToleratedBothWays(t *testing.T) {
	for _, reported := range []string{
		xray.PinnedVersion,                          // with "v"
		strings.TrimPrefix(xray.PinnedVersion, "v"), // without
	} {
		t.Run(reported, func(t *testing.T) {
			if !xray.VersionMatchesPinned(reported) {
				t.Fatalf("VersionMatchesPinned(%q) = false", reported)
			}
		})
	}
}
