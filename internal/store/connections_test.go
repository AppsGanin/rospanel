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

	var plan string
	row := st.db.QueryRow(
		`EXPLAIN QUERY PLAN
		 SELECT user_id, COUNT(DISTINCT ip) FROM connections INDEXED BY idx_connections_last_seen
		 WHERE last_seen > ? GROUP BY user_id`, 0)
	var id, parent, notused int
	if err := row.Scan(&id, &parent, &notused, &plan); err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(plan, "idx_connections_last_seen") {
		t.Fatalf("query plan %q does not use idx_connections_last_seen", plan)
	}
}

func TestActiveDeviceCountForUser(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "userconn.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	u1, err := st.CreateUser("u1", "uuid-1", "pw", "tok1", 0, 0, 0)
	if err != nil {
		t.Fatalf("create u1: %v", err)
	}
	u2, err := st.CreateUser("u2", "uuid-2", "pw", "tok2", 0, 0, 0)
	if err != nil {
		t.Fatalf("create u2: %v", err)
	}

	now := time.Now().Unix()
	_ = st.AddConnection(u1.ID, "10.0.0.1", now)
	_ = st.AddConnection(u1.ID, "10.0.0.2", now)
	_ = st.AddConnection(u1.ID, "10.0.0.3", now-500) // old
	_ = st.AddConnection(u2.ID, "10.0.0.4", now)

	count1, err := st.ActiveDeviceCountForUser(u1.ID, now-model.DeviceOnlineWindow)
	if err != nil {
		t.Fatalf("count1: %v", err)
	}
	if count1 != 2 {
		t.Errorf("u1 active devices = %d, want 2", count1)
	}

	count2, err := st.ActiveDeviceCountForUser(u2.ID, now-model.DeviceOnlineWindow)
	if err != nil {
		t.Fatalf("count2: %v", err)
	}
	if count2 != 1 {
		t.Errorf("u2 active devices = %d, want 1", count2)
	}

	// Test query plan index usage
	var plan string
	row := st.db.QueryRow(
		`EXPLAIN QUERY PLAN
		 SELECT COUNT(DISTINCT ip) FROM connections INDEXED BY idx_connections_last_seen
		 WHERE user_id = ? AND last_seen > ?`, u1.ID, 0)
	var id, parent, notused int
	if err := row.Scan(&id, &parent, &notused, &plan); err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(plan, "idx_connections_last_seen") {
		t.Fatalf("query plan %q does not use idx_connections_last_seen", plan)
	}
}
