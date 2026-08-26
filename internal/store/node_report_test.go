package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func nodeReportFixture(t *testing.T) (*Store, *model.Node, []int64) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	n, err := st.CreateNode("edge-1", "203.0.113.10", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	var ids []int64
	for i := range 3 {
		u, err := st.CreateUser(fmt.Sprintf("u%d", i), fmt.Sprintf("uuid-%d", i),
			"pw", fmt.Sprintf("tok-%d", i), 0, 0, 0)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		ids = append(ids, u.ID)
	}
	return st, n, ids
}

func nodeDeltas(nodeID int64, ids []int64, day string, up, down int64) []TrafficDelta {
	out := make([]TrafficDelta, 0, len(ids))
	for _, id := range ids {
		out = append(out, TrafficDelta{
			UserID: id, NodeID: nodeID, Day: day,
			AddUp: up, AddDown: down, SeenAt: time.Now().Unix(),
		})
	}
	return out
}

// TestApplyNodeReportAtomic: the ingest watermark is what makes a node's traffic
// idempotent, so if it advances without the traffic landing, the node's resend is
// rejected as a duplicate and that batch is lost for good. Claim and traffic must
// roll back together.
func TestApplyNodeReportAtomic(t *testing.T) {
	st, n, ids := nodeReportFixture(t)
	day := time.Now().Format("2006-01-02")

	// Abort every write to users, the way a crash mid-batch would.
	if _, err := st.db.Exec(
		`CREATE TRIGGER t_fail_users BEFORE UPDATE ON users
		 BEGIN SELECT RAISE(ABORT, 'simulated crash'); END`); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	claimed, err := st.ApplyNodeReport(n.ID, 7, nodeDeltas(n.ID, ids, day, 1000, 2000))
	if err == nil {
		t.Fatal("expected the traffic write to fail")
	}
	if claimed {
		t.Fatal("claimed must be false when the transaction rolled back")
	}
	if _, err := st.db.Exec(`DROP TRIGGER t_fail_users`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}

	// The watermark must NOT have moved, or the resend below would be dropped.
	got, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if got.LastReportID != 0 {
		t.Fatalf("watermark advanced to %d despite the rollback — the node's resend "+
			"would be rejected as a duplicate and the batch lost", got.LastReportID)
	}

	// The node resends the same report; now it counts.
	claimed, err = st.ApplyNodeReport(n.ID, 7, nodeDeltas(n.ID, ids, day, 1000, 2000))
	if err != nil {
		t.Fatalf("resend: %v", err)
	}
	if !claimed {
		t.Fatal("resend did not win the claim")
	}
	for _, id := range ids {
		u, _ := st.GetUser(id)
		if u.UsedUp != 1000 || u.UsedDown != 2000 {
			t.Fatalf("user %d traffic = %d/%d, want 1000/2000", id, u.UsedUp, u.UsedDown)
		}
	}
}

// TestApplyNodeReportCountsOnce: a replayed report (the node never saw our ack)
// must not double-count.
func TestApplyNodeReportCountsOnce(t *testing.T) {
	st, n, ids := nodeReportFixture(t)
	day := time.Now().Format("2006-01-02")

	if _, err := st.ApplyNodeReport(n.ID, 3, nodeDeltas(n.ID, ids, day, 500, 700)); err != nil {
		t.Fatalf("first: %v", err)
	}
	claimed, err := st.ApplyNodeReport(n.ID, 3, nodeDeltas(n.ID, ids, day, 500, 700))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if claimed {
		t.Fatal("replayed report won the claim — traffic would be counted twice")
	}
	for _, id := range ids {
		u, _ := st.GetUser(id)
		if u.UsedUp != 500 || u.UsedDown != 700 {
			t.Fatalf("user %d traffic = %d/%d after a replay, want 500/700", id, u.UsedUp, u.UsedDown)
		}
	}
}

// TestApplyNodeReportSurvivesDeletedUser is the batching's sharpest edge. A node
// holds unacked traffic and keeps resending it; meanwhile the auto-delete sweep
// removes one of those users. traffic_daily.user_id is a foreign key, so that row
// now violates it — and inside one transaction a single bad row would roll back the
// batch AND the watermark, so the node would resend the same poison batch every 45s
// forever and none of its traffic would ever be accounted again.
func TestApplyNodeReportSurvivesDeletedUser(t *testing.T) {
	st, n, ids := nodeReportFixture(t)
	day := time.Now().Format("2006-01-02")

	// The node's batch names a user who is gone by the time it lands.
	deltas := nodeDeltas(n.ID, ids, day, 100, 200)
	deltas = append(deltas, TrafficDelta{
		UserID: 999999, NodeID: n.ID, Day: day,
		AddUp: 100, AddDown: 200, SeenAt: time.Now().Unix(),
	})

	claimed, err := st.ApplyNodeReport(n.ID, 5, deltas)
	if err != nil {
		t.Fatalf("one departed user sank the whole batch: %v", err)
	}
	if !claimed {
		t.Fatal("watermark not claimed")
	}
	// Everyone who still exists got their traffic.
	for _, id := range ids {
		u, _ := st.GetUser(id)
		if u.UsedUp != 100 || u.UsedDown != 200 {
			t.Errorf("user %d traffic = %d/%d, want 100/200", id, u.UsedUp, u.UsedDown)
		}
	}
	got, _ := st.GetNode(n.ID)
	if got.LastReportID != 5 {
		t.Fatalf("watermark = %d, want 5 — the node would resend this batch forever",
			got.LastReportID)
	}
}

// TestAddConnectionsSurvivesDeletedUser: RecordAccess reads user ids out of the
// Xray access log, so a deleted user with a live session keeps being reported. That
// ghost must not void the batch — the sightings in it drive last_seen and the
// device cap.
func TestAddConnectionsSurvivesDeletedUser(t *testing.T) {
	st, _, ids := nodeReportFixture(t)
	now := time.Now().Unix()

	err := st.AddConnections([]ConnectionHit{
		{UserID: ids[0], IP: "1.1.1.1", SeenAt: now, Hits: 1},
		{UserID: 999999, IP: "2.2.2.2", SeenAt: now, Hits: 1}, // gone
		{UserID: ids[1], IP: "3.3.3.3", SeenAt: now, Hits: 1},
	})
	if err != nil {
		t.Fatalf("a ghost user sank the batch: %v", err)
	}
	for _, id := range []int64{ids[0], ids[1]} {
		conns, _ := st.RecentConnections(id, 10)
		if len(conns) != 1 {
			t.Errorf("user %d has %d connection rows, want 1", id, len(conns))
		}
		u, _ := st.GetUser(id)
		if u.LastSeen != now {
			t.Errorf("user %d last_seen = %d, want %d", id, u.LastSeen, now)
		}
	}
}

// TestNodeTrafficLeavesLocalBaseline is the trap the Baseline field exists to
// prevent: a node's numbers must never be written into last_up/last_down, which
// track the MASTER's own Xray counters. If they were, the next local poll would
// subtract against a foreign number and mis-account the master's traffic.
func TestNodeTrafficLeavesLocalBaseline(t *testing.T) {
	st, n, ids := nodeReportFixture(t)
	day := time.Now().Format("2006-01-02")

	// The local poller has recorded where it last read Xray's counters.
	if err := st.UpdateTraffic(ids[0], 0, 0, 111, 222); err != nil {
		t.Fatalf("seed baseline: %v", err)
	}

	if _, err := st.ApplyNodeReport(n.ID, 1, nodeDeltas(n.ID, ids, day, 9000, 9000)); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	u, err := st.GetUser(ids[0])
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.LastUp != 111 || u.LastDown != 222 {
		t.Fatalf("node ingest overwrote the local counter baseline: last_up/last_down "+
			"= %d/%d, want 111/222", u.LastUp, u.LastDown)
	}
	if u.UsedUp != 9000 {
		t.Errorf("node traffic not accumulated: used_up = %d", u.UsedUp)
	}
}

// The node traffic coefficient must scale the USER'S quota but not the per-node
// statistics: a coefficient of 2 charges the allowance double while the byte counters
// stay real (and 0.5 the reverse). If the two were ever charged the same value, either
// billing would be wrong or the infrastructure stats would lie.
func TestNodeCoefficientScalesQuotaNotStats(t *testing.T) {
	st, n, ids := nodeReportFixture(t)
	u := ids[0]
	const up, down = 1000, 4000

	// Charge quota at 2x, book stats at face value — the shape the manager produces.
	deltas := []TrafficDelta{{
		UserID: u, NodeID: n.ID, Day: "2026-01-01",
		AddUp: up, AddDown: down,
		QuotaUp: 2 * up, QuotaDown: 2 * down,
		SeenAt: time.Now().Unix(),
	}}
	if _, err := st.ApplyNodeReport(n.ID, 1, deltas); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Quota: the user's lifetime totals reflect the scaled bytes.
	usr, err := st.GetUser(u)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if usr.UsedUp != 2*up || usr.UsedDown != 2*down {
		t.Errorf("quota = %d/%d, want the doubled %d/%d", usr.UsedUp, usr.UsedDown, 2*up, 2*down)
	}

	// Per-node stats: the real bytes, unscaled.
	totals, err := st.NodeTrafficTotals(u, "2026-01-01", "2026-01-01")
	if err != nil {
		t.Fatalf("node totals: %v", err)
	}
	if got := totals[n.ID]; got[0] != up || got[1] != down {
		t.Errorf("per-node stats = %d/%d, want the real %d/%d (coefficient must not touch these)",
			got[0], got[1], up, down)
	}
}

// A delta with no explicit quota (the local poller, or any pre-coefficient caller)
// charges the real bytes — the coefficient feature must not silently zero quota for
// everyone who didn't set it.
func TestTrafficDeltaWithoutQuotaChargesRealBytes(t *testing.T) {
	st, n, ids := nodeReportFixture(t)
	u := ids[0]
	deltas := []TrafficDelta{{
		UserID: u, NodeID: n.ID, Day: "2026-01-02",
		AddUp: 500, AddDown: 700, SeenAt: time.Now().Unix(),
	}}
	if _, err := st.ApplyNodeReport(n.ID, 1, deltas); err != nil {
		t.Fatalf("apply: %v", err)
	}
	usr, _ := st.GetUser(u)
	if usr.UsedUp != 500 || usr.UsedDown != 700 {
		t.Errorf("quota = %d/%d, want the real 500/700 when no coefficient is set", usr.UsedUp, usr.UsedDown)
	}
}
