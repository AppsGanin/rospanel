package store

import (
	"testing"
	"time"
)

// The first-connection start, at the level where its one real guarantee lives: a
// date, once there is one, is never written over by a connection — however the row
// came to carry a stale hold beside it.
func TestAConnectionNeverOverwritesADate(t *testing.T) {
	st := newStore(t)
	u, err := st.CreateUserOnHold("held", "uuid-held", "pw", "tok-held", 0, 30*86400)
	if err != nil {
		t.Fatal(err)
	}
	date := time.Now().Add(72 * time.Hour).Unix()
	// A row no write path produces any more, forced here: a hold AND a date.
	if _, err := st.db.Exec(`UPDATE users SET expire_at = ?, hold_seconds = 86400 WHERE id = ?`, date, u.ID); err != nil {
		t.Fatal(err)
	}
	started, err := st.RecordConnections([]ConnectionHit{{UserID: u.ID, IP: "198.51.100.1", SeenAt: time.Now().Unix(), Hits: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 0 {
		t.Errorf("a user with a date was reported as starting a term: %+v", started)
	}
	if got, _ := st.GetUser(u.ID); got.ExpireAt != date {
		t.Errorf("the date moved from %d to %d", date, got.ExpireAt)
	}
}

// The term runs from the sighting, not from whenever the batch happens to be written:
// a flush that lands late must not hand the user extra days.
func TestATermRunsFromTheSighting(t *testing.T) {
	st := newStore(t)
	u, err := st.CreateUserOnHold("late", "uuid-late", "pw", "tok-late", 0, 10*86400)
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Now().Add(-40 * time.Second).Unix()
	started, err := st.RecordConnections([]ConnectionHit{{UserID: u.ID, IP: "198.51.100.2", SeenAt: seen, Hits: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0].UserID != u.ID || started[0].ExpireAt != seen+10*86400 || started[0].HoldSeconds != 10*86400 {
		t.Fatalf("started %+v, want user %d to end at %d after a 10-day term", started, u.ID, seen+10*86400)
	}
	if got, _ := st.GetUser(u.ID); got.ExpireAt != seen+10*86400 || got.HoldSeconds != 0 {
		t.Errorf("stored expire %d hold %d", got.ExpireAt, got.HoldSeconds)
	}
}

// The check reads the held users through their own index: it runs on every flush of
// the access log, and a full scan of users there would grow with the whole roster.
func TestHeldUsersAreFoundThroughTheirIndex(t *testing.T) {
	st := newStore(t)
	rows, err := st.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM users INDEXED BY idx_users_held WHERE hold_seconds > 0`)
	if err != nil {
		t.Fatalf("the query the flush runs does not plan: %v", err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "; "
	}
	if plan == "" {
		t.Fatal("empty query plan")
	}
	t.Logf("plan: %s", plan)
}
