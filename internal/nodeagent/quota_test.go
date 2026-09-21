package nodeagent

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

func quotaAgent() *Agent {
	return &Agent{
		lastCounters: map[string]xray.Traffic{},
		pending:      map[int64]*nodeapi.TrafficDelta{},
		inflight:     map[int64]*nodeapi.TrafficDelta{},
		quotaActive:  map[int64]time.Time{},
		quotaAsked:   map[int64]struct{}{},
	}
}

// Everything the panel has not seen counts: not sampled yet, sampled and waiting, sent
// and not acknowledged. The user who ran out is flagged and dropped from the watch
// until the panel answers again; the one with room left is not.
func TestQuotaCheckCountsWhatThePanelHasNotSeen(t *testing.T) {
	a := quotaAgent()
	a.lastCounters["u1"] = xray.Traffic{Up: 1000, Down: 1000}
	a.lastCounters["u2"] = xray.Traffic{Up: 1000, Down: 1000}
	a.pending[1] = &nodeapi.TrafficDelta{UserID: 1, Down: 300}
	a.inflight[1] = &nodeapi.TrafficDelta{UserID: 1, Down: 300}
	a.quotaLeft = map[int64]int64{1: 1000, 2: 1000}

	stats := map[string]xray.Traffic{
		"u1": {Up: 1100, Down: 1200}, // 300 unsampled + 600 waiting = 900
		"u2": {Up: 1100, Down: 1100}, // 200
	}
	if crossed, _ := a.quotaCheckLocked(stats); crossed {
		t.Fatal("900 of 1000 counted as run out")
	}
	stats["u1"] = xray.Traffic{Up: 1200, Down: 1200} // 400 + 600 = 1000
	if crossed, _ := a.quotaCheckLocked(stats); !crossed || !a.quotaCrossed {
		t.Fatal("1000 of 1000 not counted as run out")
	}
	if _, watched := a.quotaLeft[1]; watched {
		t.Fatal("the user who ran out is still watched")
	}
	if _, watched := a.quotaLeft[2]; !watched {
		t.Fatal("the user with room left stopped being watched")
	}
	if _, ok := a.quotaActive[1]; !ok {
		t.Fatal("a user whose counters moved is not marked active")
	}
}

// A counter below the last sample means Xray restarted: what it shows is all new.
func TestQuotaCheckAcrossAnXrayRestart(t *testing.T) {
	a := quotaAgent()
	a.lastCounters["u1"] = xray.Traffic{Up: 5000, Down: 5000}
	a.quotaLeft = map[int64]int64{1: 1000}
	if crossed, _ := a.quotaCheckLocked(map[string]xray.Traffic{"u1": {Up: 300, Down: 400}}); crossed {
		t.Fatal("700 since a restart counted as 1000")
	}
	if crossed, _ := a.quotaCheckLocked(map[string]xray.Traffic{"u1": {Up: 600, Down: 500}}); !crossed {
		t.Fatal("1100 since a restart not counted as run out")
	}
}

// Every request asks about everyone active lately or with traffic the panel has not
// acknowledged — the same users request after request, so every answer, held or not,
// covers them. It carries the crossing mark once, and is new only when it names
// someone the last one did not, or when the last one went unanswered.
func TestTakeQuotaUsers(t *testing.T) {
	a := quotaAgent()
	now := time.Now()
	a.quotaActive[3] = now
	a.pending[4] = &nodeapi.TrafficDelta{UserID: 4, Up: 1}
	a.inflight[5] = &nodeapi.TrafficDelta{UserID: 5, Up: 1}
	a.quotaCrossed = true
	users, crossed, fresh := a.takeQuotaUsers(true)
	slices.Sort(users)
	if !slices.Equal(users, []int64{3, 4, 5}) || !crossed || !fresh {
		t.Fatalf("asked about %v crossed=%v new=%v, want [3 4 5], the mark, and new", users, crossed, fresh)
	}
	users, crossed, fresh = a.takeQuotaUsers(true)
	slices.Sort(users)
	if !slices.Equal(users, []int64{3, 4, 5}) || crossed || fresh {
		t.Fatalf("second request asked about %v crossed=%v new=%v, want the same [3 4 5], nothing new", users, crossed, fresh)
	}
	a.quotaActive[3] = now.Add(-quotaActiveFor)
	users, _, fresh = a.takeQuotaUsers(true)
	slices.Sort(users)
	if !slices.Equal(users, []int64{4, 5}) || fresh {
		t.Fatalf("with user 3 idle asked about %v new=%v, want [4 5], nothing new", users, fresh)
	}
	a.quotaActive[6] = now
	if _, _, fresh = a.takeQuotaUsers(true); !fresh {
		t.Fatal("a user the last request did not name did not make the request new")
	}
	a.forgetQuotaAsked()
	if _, _, fresh = a.takeQuotaUsers(true); !fresh {
		t.Fatal("the request after an unanswered one is not new")
	}
}

// The crossing mark rides every request until one carries the traffic sampled when
// the user ran out: a request resending an older report keeps it on.
func TestCrossingMarkWaitsForTheCrossingTraffic(t *testing.T) {
	dir := t.TempDir()
	a := quotaAgent()
	a.dataDir, a.state, a.certPath = dir, &persistState{}, filepath.Join(dir, "cert.pem")
	a.sup = xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	a.reportSeq, a.inflightID = 4, 4
	a.inflight[1] = &nodeapi.TrafficDelta{UserID: 1, Down: 100} // sent before the crossing
	a.pending[1] = &nodeapi.TrafficDelta{UserID: 1, Down: 900}  // sampled at the crossing
	a.quotaCrossed = true

	req := a.buildSyncRequest()
	if req.ReportID != 4 || !req.QuotaCrossed {
		t.Fatalf("resend of report %d crossed=%v, want report 4 with the mark", req.ReportID, req.QuotaCrossed)
	}
	a.ackReport(4)
	req = a.buildSyncRequest()
	if req.ReportID != 5 || len(req.Traffic) != 1 || req.Traffic[0].Down != 900 || !req.QuotaCrossed {
		t.Fatalf("report %d %+v crossed=%v, want report 5 with the crossing's 900 and the mark", req.ReportID, req.Traffic, req.QuotaCrossed)
	}
	a.ackReport(5)
	if req = a.buildSyncRequest(); req.QuotaCrossed {
		t.Fatal("the mark outlived the report that carried the crossing")
	}
}

// A user whose traffic moves and whom no request has named yet is a newcomer, which
// cuts the poll's wait; once a request names them, they are not.
func TestQuotaCheckFindsNewcomers(t *testing.T) {
	a := quotaAgent()
	a.lastCounters["u7"] = xray.Traffic{Up: 10, Down: 10}
	stats := map[string]xray.Traffic{"u7": {Up: 10, Down: 10}}
	if _, newcomer := a.quotaCheckLocked(stats); newcomer {
		t.Fatal("a user whose counters did not move is a newcomer")
	}
	stats["u7"] = xray.Traffic{Up: 20, Down: 10}
	if _, newcomer := a.quotaCheckLocked(stats); !newcomer {
		t.Fatal("a user who started using the node is not a newcomer")
	}
	if users, _, _ := a.takeQuotaUsers(true); !slices.Contains(users, 7) {
		t.Fatalf("the next request asked about %v, not the newcomer", users)
	}
	if _, newcomer := a.quotaCheckLocked(stats); newcomer {
		t.Fatal("a user the last request named is still a newcomer")
	}
}

// The acknowledgement and the new figures land together.
func TestSettleReport(t *testing.T) {
	a := quotaAgent()
	a.inflight[1] = &nodeapi.TrafficDelta{UserID: 1, Down: 300}
	a.inflightID = 9
	a.quotaLeft = map[int64]int64{1: 1000}
	a.settleReport(9, map[int64]int64{1: 700})
	if len(a.inflight) != 0 || a.quotaLeft[1] != 700 {
		t.Fatalf("inflight %v quota %v, want the report acknowledged and 700 left", a.inflight, a.quotaLeft)
	}
	a.settleReport(0, nil)
	if a.quotaLeft != nil {
		t.Fatal("an answer without a quota left the old one watched")
	}
}

// The newcomer's cut ends a poll only while it waits: an answer already on its way in
// is let through.
func TestInterruptWaitSparesAnAnswerOnItsWay(t *testing.T) {
	a := quotaAgent()
	cancelled := false
	a.syncCancel = func() { cancelled = true }
	a.interruptWait()
	if cancelled || a.syncInterrupted.Load() {
		t.Fatal("a poll whose answer was coming in was cut")
	}
	a.syncWaiting = true
	a.interruptWait()
	if !cancelled || !a.syncInterrupted.Load() {
		t.Fatal("a waiting poll was not cut")
	}
}
