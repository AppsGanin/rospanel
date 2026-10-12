package nodeagent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Lane traffic rides the users' report: a report with nothing but lanes still goes out
// once it has waited long enough, is resent under the same id until acknowledged, and
// lanes sampled meanwhile wait for the next report instead of joining one in flight.
func TestLanesRideTheReport(t *testing.T) {
	defer func(d time.Duration) { lanesAlone = d }(lanesAlone)
	lanesAlone = 0
	dir := t.TempDir()
	a := &Agent{
		dataDir:      dir,
		sup:          xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir),
		certPath:     filepath.Join(dir, "cert.pem"),
		state:        &persistState{},
		pending:      map[int64]*nodeapi.TrafficDelta{},
		inflight:     map[int64]*nodeapi.TrafficDelta{},
		lastCounters: map[string]xray.Traffic{},
	}
	a.statsMu.Lock()
	a.addLaneLocked("naive-in", 100, 1000)
	a.addLaneLocked("naive-in", 1, 10)
	a.addLaneLocked(awgLane, 0, 0) // nothing moved: no entry
	a.statsMu.Unlock()

	req := a.buildSyncRequest()
	if req.ReportID == 0 || len(req.Lanes) != 1 || req.Lanes[0] != (nodeapi.LaneDelta{Lane: "naive-in", Up: 101, Down: 1010}) {
		t.Fatalf("lanes-only report: id %d lanes %+v", req.ReportID, req.Lanes)
	}
	a.statsMu.Lock()
	a.addLaneLocked(awgLane, 5, 50)
	a.statsMu.Unlock()
	again := a.buildSyncRequest()
	if again.ReportID != req.ReportID || len(again.Lanes) != 1 || again.Lanes[0].Lane != "naive-in" {
		t.Fatalf("resend changed: id %d → %d, lanes %+v", req.ReportID, again.ReportID, again.Lanes)
	}
	a.ackReport(req.ReportID)
	next := a.buildSyncRequest()
	if next.ReportID == req.ReportID || len(next.Lanes) != 1 || next.Lanes[0] != (nodeapi.LaneDelta{Lane: awgLane, Up: 5, Down: 50}) {
		t.Fatalf("after the ack: id %d lanes %+v", next.ReportID, next.Lanes)
	}
}

// Lane traffic alone waits for user traffic to ride with, rather than going out every
// sample; user traffic takes it along at once.
func TestLanesWaitForUserTraffic(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{
		dataDir: dir, sup: xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir),
		certPath: filepath.Join(dir, "cert.pem"), state: &persistState{},
		pending: map[int64]*nodeapi.TrafficDelta{}, inflight: map[int64]*nodeapi.TrafficDelta{},
		lastCounters: map[string]xray.Traffic{},
	}
	a.statsMu.Lock()
	a.addLaneLocked("vless-in", 1, 2)
	a.statsMu.Unlock()
	if req := a.buildSyncRequest(); req.ReportID != 0 || len(req.Lanes) != 0 {
		t.Fatalf("lanes alone went out at once: %+v", req)
	}
	a.statsMu.Lock()
	a.pending[7] = &nodeapi.TrafficDelta{UserID: 7, Up: 10, Down: 20}
	a.statsMu.Unlock()
	if req := a.buildSyncRequest(); req.ReportID == 0 || len(req.Lanes) != 1 || len(req.Traffic) != 1 {
		t.Fatalf("user traffic did not take the lanes along: %+v", req)
	}
}

// A report in flight with lanes alone is not "nothing in flight": a user who ran out of
// quota meanwhile keeps the crossed flag until their traffic goes out with it.
func TestQuotaCrossedWaitsBehindALanesReport(t *testing.T) {
	defer func(d time.Duration) { lanesAlone = d }(lanesAlone)
	lanesAlone = 0
	dir := t.TempDir()
	a := &Agent{
		dataDir: dir, sup: xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir),
		certPath: filepath.Join(dir, "cert.pem"), state: &persistState{},
		pending: map[int64]*nodeapi.TrafficDelta{}, inflight: map[int64]*nodeapi.TrafficDelta{},
		lastCounters: map[string]xray.Traffic{},
	}
	a.statsMu.Lock()
	a.addLaneLocked("vless-in", 1, 2)
	a.statsMu.Unlock()
	lanesOnly := a.buildSyncRequest()
	if lanesOnly.ReportID == 0 || len(lanesOnly.Traffic) != 0 {
		t.Fatalf("lanes-only report: %+v", lanesOnly)
	}
	a.statsMu.Lock()
	a.pending[7] = &nodeapi.TrafficDelta{UserID: 7, Up: 10, Down: 20}
	a.quotaCrossed = true
	a.statsMu.Unlock()
	resend := a.buildSyncRequest()
	if resend.ReportID != lanesOnly.ReportID || len(resend.Traffic) != 0 {
		t.Fatalf("resend: %+v", resend)
	}
	a.ackReport(lanesOnly.ReportID)
	next := a.buildSyncRequest()
	if len(next.Traffic) != 1 || !next.QuotaCrossed {
		t.Fatalf("the crossing went out without its traffic: traffic %d crossed %v", len(next.Traffic), next.QuotaCrossed)
	}
}
