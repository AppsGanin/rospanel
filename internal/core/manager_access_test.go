package core

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/store"
)

func accessTestManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "acc.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Manager{
		store:      st,
		accLast:    make(map[string]int64),
		accPending: make(map[accPendingKey]store.ConnectionHit),
	}, st
}

// TestRecordAccessDoesNoIO pins the hot path. RecordAccess runs for every line the
// Xray access log emits; it must only touch memory, leaving the write to the
// flusher. Before this, each admitted sighting did two statements plus a full
// WorkingUsers query — the panel's single busiest write source.
func TestRecordAccessDoesNoIO(t *testing.T) {
	m, st := accessTestManager(t)
	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	email := fmt.Sprintf("u%d", u.ID)

	for range 50 {
		m.RecordAccess(email, "1.1.1.1")
		m.RecordAccess(email, "2.2.2.2")
	}

	conns, err := st.RecentConnections(u.ID, 10)
	if err != nil {
		t.Fatalf("connections: %v", err)
	}
	if len(conns) != 0 {
		t.Fatalf("RecordAccess wrote %d rows before the flush — the hot path is still doing I/O", len(conns))
	}
	// The 10s throttle collapses the burst: two IPs, one buffered sighting each.
	if got := len(m.accPending); got != 2 {
		t.Fatalf("buffered %d sightings from 100 calls across 2 IPs, want 2", got)
	}
}

// TestFlushAccessWritesBatch: the buffered sightings land, and flushing an empty
// buffer is free.
func TestFlushAccessWritesBatch(t *testing.T) {
	m, st := accessTestManager(t)
	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	email := fmt.Sprintf("u%d", u.ID)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		m.RecordAccess(email, ip)
	}

	m.FlushAccess()
	conns, err := st.RecentConnections(u.ID, 10)
	if err != nil {
		t.Fatalf("connections: %v", err)
	}
	if len(conns) != 3 {
		t.Fatalf("flushed %d connection rows, want 3", len(conns))
	}
	cur, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if cur.LastSeen == 0 {
		t.Error("flush did not stamp last_seen")
	}

	// Buffer is drained, so a second flush writes nothing new.
	if len(m.accPending) != 0 {
		t.Fatalf("buffer still holds %d sightings after a flush", len(m.accPending))
	}
	m.FlushAccess()
	conns, _ = st.RecentConnections(u.ID, 10)
	if len(conns) != 3 {
		t.Fatalf("second flush changed the row count to %d", len(conns))
	}
}

// TestRecordAccessIgnoresJunk: the access log is parsed text, so non-user emails
// must not create buffer entries.
func TestRecordAccessIgnoresJunk(t *testing.T) {
	m, _ := accessTestManager(t)
	for _, email := range []string{"", "admin", "unotanumber", "12", "u"} {
		m.RecordAccess(email, "1.1.1.1")
	}
	if len(m.accPending) != 0 {
		t.Fatalf("buffered %d sightings from junk emails", len(m.accPending))
	}
}
