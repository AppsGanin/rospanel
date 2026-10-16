package nodeagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Traffic the panel has not acknowledged survives a stop: what was in flight goes
// out again under its own report id (the panel drops it if it already took it), and
// what was never sent goes out as new.
func TestTrafficKeptAcrossStop(t *testing.T) {
	dir := t.TempDir()
	mk := func() *Agent {
		return &Agent{
			dataDir:      dir,
			sup:          xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir),
			certPath:     filepath.Join(dir, "cert.pem"),
			state:        &persistState{},
			pending:      map[int64]*nodeapi.TrafficDelta{},
			inflight:     map[int64]*nodeapi.TrafficDelta{},
			lastCounters: map[string]xray.Traffic{},
		}
	}
	a := mk()
	a.pending[7] = &nodeapi.TrafficDelta{UserID: 7, Up: 10, Down: 20}
	sent := a.buildSyncRequest() // 7 is in flight now, unacknowledged
	a.pending[8] = &nodeapi.TrafficDelta{UserID: 8, Down: 5}
	a.pendingLanes = map[string]*nodeapi.LaneDelta{"vless": {Lane: "vless", Up: 1, Down: 2}}
	a.keepTrafficOnStop()

	b := mk()
	b.reportSeq = a.state.LastReportID
	b.restoreCarry()
	if _, err := os.Stat(carryPath(dir)); !os.IsNotExist(err) {
		t.Error("the kept traffic was not removed once restored: a second start would count it twice")
	}
	again := b.buildSyncRequest()
	if again.ReportID != sent.ReportID || len(again.Traffic) != 1 || again.Traffic[0].UserID != 7 || again.Traffic[0].Down != 20 {
		t.Fatalf("in-flight batch not resent as it was: %+v (first sent as %d)", again, sent.ReportID)
	}
	b.ackReport(again.ReportID)
	next := b.buildSyncRequest()
	if next.ReportID <= sent.ReportID || len(next.Traffic) != 1 || next.Traffic[0].UserID != 8 || next.Traffic[0].Down != 5 {
		t.Fatalf("unsent traffic not sent as a new batch: %+v", next)
	}
	if len(next.Lanes) != 1 || next.Lanes[0].Lane != "vless" || next.Lanes[0].Down != 2 {
		t.Errorf("lane traffic lost: %+v", next.Lanes)
	}

	// Nothing to keep: no file.
	c := mk()
	c.keepTrafficOnStop()
	if _, err := os.Stat(carryPath(dir)); !os.IsNotExist(err) {
		t.Error("an empty carry was written")
	}
}

// A damaged file's empty entries are dropped, and the restored batch's id is never
// handed out again.
func TestRestoreCarryDamagedAndID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(carryPath(dir), []byte(`{"inflight_id":41,"inflight":{"7":{"user_id":7,"up":3},"8":null},"pending":{"9":null}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		dataDir: dir, state: &persistState{LastReportID: 12},
		pending: map[int64]*nodeapi.TrafficDelta{}, inflight: map[int64]*nodeapi.TrafficDelta{},
	}
	a.reportSeq = a.state.LastReportID
	a.restoreCarry()
	if len(a.inflight) != 1 || a.inflight[7] == nil || len(a.pending) != 0 {
		t.Errorf("restored %+v / %+v", a.inflight, a.pending)
	}
	if a.state.LastReportID != 41 || a.reportSeq != 41 {
		t.Errorf("report id after restore: state %d, seq %d — want 41", a.state.LastReportID, a.reportSeq)
	}
}

// Kept traffic that cannot be removed is dropped rather than restored by every start.
func TestRestoreCarryNotRemovable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes the file regardless of the directory's mode")
	}
	dir := t.TempDir()
	if err := os.WriteFile(carryPath(dir), []byte(`{"pending":{"7":{"user_id":7,"up":3}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	a := &Agent{dataDir: dir, state: &persistState{}, pending: map[int64]*nodeapi.TrafficDelta{}}
	a.restoreCarry()
	if len(a.pending) != 0 {
		t.Errorf("restored traffic whose file stays: %+v", a.pending)
	}
}
