package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestPurgeTrafficDaily(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "traffic.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	day := func(ago int) string {
		return time.Now().AddDate(0, 0, -ago).Format("2006-01-02")
	}
	// Two nodes per day, so the sweep has to clear both rows of an expired day and
	// not just the local one.
	for _, ago := range []int{0, 1, model.TrafficDailyRetentionDays - 1} {
		for _, node := range []int64{model.LocalNodeID, 7} {
			if err := st.AddDailyTrafficNode(u.ID, node, day(ago), 100, 200); err != nil {
				t.Fatalf("add kept traffic: %v", err)
			}
		}
	}
	for _, ago := range []int{model.TrafficDailyRetentionDays + 1, model.TrafficDailyRetentionDays * 2} {
		for _, node := range []int64{model.LocalNodeID, 7} {
			if err := st.AddDailyTrafficNode(u.ID, node, day(ago), 100, 200); err != nil {
				t.Fatalf("add stale traffic: %v", err)
			}
		}
	}

	cutoff := day(model.TrafficDailyRetentionDays)
	n, err := st.PurgeTrafficDaily(cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 4 {
		t.Fatalf("purged %d rows, want 4", n)
	}

	// Everything inside the window survives, with its totals intact.
	pts, err := st.StatsSeries(u.ID, "2000-01-01", day(0))
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(pts) != 3 {
		t.Fatalf("kept %d days, want 3", len(pts))
	}
	for _, p := range pts {
		if p.Day < cutoff {
			t.Errorf("day %s is older than the cutoff %s", p.Day, cutoff)
		}
		if p.Up != 200 || p.Down != 400 { // both nodes summed
			t.Errorf("day %s: up=%d down=%d, want 200/400", p.Day, p.Up, p.Down)
		}
	}

	// Idempotent: a second sweep over the same cutoff has nothing left to do.
	again, err := st.PurgeTrafficDaily(cutoff)
	if err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if again != 0 {
		t.Fatalf("second purge removed %d rows, want 0", again)
	}
}

// TestPurgeTrafficDailyBatches covers the chunking loop: a backlog larger than
// purgeBatch must come out in full, not one batch's worth.
func TestPurgeTrafficDailyBatches(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "traffic-batch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	const stale = purgeBatch + 42
	base := time.Now().AddDate(0, 0, -model.TrafficDailyRetentionDays-1)
	for i := 0; i < stale; i++ {
		day := base.AddDate(0, 0, -i).Format("2006-01-02")
		if err := st.AddDailyTrafficNode(u.ID, model.LocalNodeID, day, 1, 1); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	n, err := st.PurgeTrafficDaily(time.Now().AddDate(0, 0, -model.TrafficDailyRetentionDays).Format("2006-01-02"))
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != stale {
		t.Fatalf("purged %d rows, want %d", n, stale)
	}
}
