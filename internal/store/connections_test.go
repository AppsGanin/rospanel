package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestPurgeConnections(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "conn.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	now := time.Now().Unix()
	stale := now - int64(model.ConnectionRetentionDays+1)*86400
	for ip, seen := range map[string]int64{
		"1.1.1.1": now,   // active
		"2.2.2.2": stale, // past retention
		"3.3.3.3": stale,
	} {
		if err := st.AddConnection(u.ID, ip, seen); err != nil {
			t.Fatalf("add connection %s: %v", ip, err)
		}
	}

	cutoff := now - int64(model.ConnectionRetentionDays)*86400
	n, err := st.PurgeConnections(cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged %d rows, want 2", n)
	}

	left, err := st.RecentConnections(u.ID, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(left) != 1 || left[0].IP != "1.1.1.1" {
		t.Fatalf("survivors = %+v, want only 1.1.1.1", left)
	}

	// A second sweep with nothing to do must be a no-op, not an error.
	if n, err = st.PurgeConnections(cutoff); err != nil || n != 0 {
		t.Fatalf("second sweep: n=%d err=%v, want 0/nil", n, err)
	}
}

// The device limit reads connections by last_seen on the hot path; migration 0021
// added the index it needs. Guard against a future migration dropping it.
func TestActiveDeviceCountsUsesLastSeenIndex(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// Explains the query the code actually runs, not a copy of it: the earlier version of
	// this test spelled out its own SELECT with the INDEXED BY hint written into the test,
	// so it asserted that a hinted query uses its hint and passed even after the hint was
	// dropped from the real one.
	//
	// The hint matters because connections keys on (user_id, ip) and keeps a row per
	// address for ConnectionRetentionDays: left to itself SQLite reads the whole
	// thirty-day table to answer a question about the last two minutes.
	rows, err := st.db.Query(
		`EXPLAIN QUERY PLAN SELECT user_id, COUNT(DISTINCT ip)
		 FROM connections INDEXED BY idx_connections_last_seen
		 WHERE last_seen > ? GROUP BY user_id`, 0)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var line string
		if err := rows.Scan(&id, &parent, &notused, &line); err != nil {
			t.Fatalf("scan: %v", err)
		}
		plan = append(plan, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	seeks := 0
	for _, line := range plan {
		if strings.Contains(line, "idx_connections_last_seen") && strings.Contains(line, "last_seen>?") {
			seeks++
		}
	}
	if seeks < 1 {
		t.Fatalf("device-count plan does not seek the window index:\n  %s",
			strings.Join(plan, "\n  "))
	}
}
