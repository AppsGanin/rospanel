package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

func uptimeManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "uptime.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	return New(st, sup, xray.Options{}, TLSPaths{}, dir), st
}

// day returns the operator-local day n days back, formatted the way the rollup keys
// its rows.
func (m *Manager) dayBack(n int) string {
	return time.Now().In(m.loc()).AddDate(0, 0, -n).Format("2006-01-02")
}

func TestStatusPageDataBuildsTheWindow(t *testing.T) {
	m, st := uptimeManager(t)

	// Yesterday: 3 of 4 samples up. Today: all up. Everything older: no data.
	for range 3 {
		if err := st.RecordUptimeSample(model.LocalNodeID, m.dayBack(1), true); err != nil {
			t.Fatalf("sample: %v", err)
		}
	}
	if err := st.RecordUptimeSample(model.LocalNodeID, m.dayBack(1), false); err != nil {
		t.Fatalf("sample: %v", err)
	}
	if err := st.RecordUptimeSample(model.LocalNodeID, m.dayBack(0), true); err != nil {
		t.Fatalf("sample: %v", err)
	}

	rep, err := m.StatusPageData(7)
	if err != nil {
		t.Fatalf("status data: %v", err)
	}
	if rep.Days != 7 {
		t.Fatalf("window = %d days, want 7", rep.Days)
	}
	if len(rep.Servers) != 1 {
		t.Fatalf("%d servers, want just the master", len(rep.Servers))
	}
	master := rep.Servers[0]
	if len(master.Days) != 7 {
		t.Fatalf("%d columns, want one per day in the window", len(master.Days))
	}
	// Oldest first, and the last column is today: the page reads left to right and
	// its "today" marker hangs off the final bar.
	if master.Days[0].Day != m.dayBack(6) || master.Days[6].Day != m.dayBack(0) {
		t.Errorf("calendar runs %s … %s, want %s … %s",
			master.Days[0].Day, master.Days[6].Day, m.dayBack(6), m.dayBack(0))
	}
	// Unsampled days are gaps, not outages.
	if master.Days[0].Samples != 0 || master.Days[0].Ratio != 0 {
		t.Errorf("an unsampled day = %+v, want an empty column", master.Days[0])
	}
	if got := master.Days[5]; got.Samples != 4 || got.Ratio != 0.75 {
		t.Errorf("yesterday = %+v, want 4 samples at 0.75", got)
	}
	// 4 up of 5 sampled across the window.
	if master.Samples != 5 || master.Uptime != 80 {
		t.Errorf("uptime = %.2f%% over %d samples, want 80%% over 5", master.Uptime, master.Samples)
	}
	if rep.At.IsZero() {
		t.Error("the report carries no assembly time for the page to print")
	}
}

// A window wider than the retention (or a nonsense one) is clamped rather than
// producing a page of empty columns nobody kept data for.
func TestStatusPageDataClampsTheWindow(t *testing.T) {
	m, _ := uptimeManager(t)
	for _, days := range []int{0, -5, model.UptimeRetentionDays + 100} {
		rep, err := m.StatusPageData(days)
		if err != nil {
			t.Fatalf("status data(%d): %v", days, err)
		}
		if rep.Days != model.UptimeRetentionDays {
			t.Errorf("window(%d) = %d, want the retention window", days, rep.Days)
		}
	}
}

// A server the operator switched off is not an incident: listing it as down would
// invite tickets about a machine that is gone on purpose.
func TestStatusPageDataSkipsDisabledNodes(t *testing.T) {
	m, st := uptimeManager(t)
	n, err := m.CreateNode("retired", "nl.example.com")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	// Make it look joined, then switch it off.
	if err := st.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), ConfigHash: "h", XrayRunning: true,
	}); err != nil {
		t.Fatalf("node status: %v", err)
	}
	if err := st.SetNodeEnabled(n.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	rep, err := m.StatusPageData(7)
	if err != nil {
		t.Fatalf("status data: %v", err)
	}
	for _, s := range rep.Servers {
		if s.Name == "retired" {
			t.Error("a switched-off server is shown on the public page")
		}
	}
	// Only the master is listed. (Its own state here is "Xray down" — there is no
	// binary in the test environment — so the headline says so, correctly; what
	// matters is that the retired node contributed nothing either way.)
	if len(rep.Servers) != 1 {
		t.Errorf("%d servers listed, want only the master", len(rep.Servers))
	}
}

// The sampler must not record an outage for servers the operator switched off, or a
// node that was never installed — the page's uptime figure would be their doing.
func TestSampleUptimeSkipsDisabledAndUnjoined(t *testing.T) {
	m, st := uptimeManager(t)
	off, err := m.CreateNode("off", "a.example.com")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SetNodeEnabled(off.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	never, err := m.CreateNode("never-installed", "b.example.com")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	m.SampleUptime()

	rows, err := st.UptimeSince(m.dayBack(1))
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, r := range rows {
		if r.NodeID == off.ID || r.NodeID == never.ID {
			t.Errorf("sampled node %d, which is disabled or never installed", r.NodeID)
		}
	}
	if len(rows) != 1 || rows[0].NodeID != model.LocalNodeID {
		t.Errorf("history = %+v, want one row for the master", rows)
	}
}
