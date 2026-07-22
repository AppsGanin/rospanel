package store

import (
	"path/filepath"
	"testing"
)

func abuseTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "abuse.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestAbuseRollupAndReads(t *testing.T) {
	st := abuseTestStore(t)
	u, err := st.CreateUser("u1", "uuid1", "pw", "tok1", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Two hits on the same (user, node, domain, day) must fold to one row, count 3.
	if err := st.AddAbuseMatches([]AbuseHit{
		{UserID: u.ID, Domain: "evil.example", Category: "malware", Day: "2026-07-20", Count: 2, SeenAt: 100},
		{UserID: u.ID, Domain: "evil.example", Category: "malware", Day: "2026-07-20", Count: 1, SeenAt: 150},
		{UserID: u.ID, Domain: "casino.example", Category: "gambling", Day: "2026-07-20", Count: 5, SeenAt: 120},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	rows, err := st.AbuseByUser(u.ID, 10)
	if err != nil {
		t.Fatalf("by user: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rolled-up rows, got %d: %+v", len(rows), rows)
	}
	// Newest last_seen first.
	if rows[0].Domain != "evil.example" || rows[0].Count != 3 || rows[0].LastSeen != 150 {
		t.Fatalf("rollup wrong: %+v", rows[0])
	}

	counts, err := st.AbuseUserCountsForDay("2026-07-20")
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts[u.ID] != 8 {
		t.Fatalf("day count = %d, want 8", counts[u.ID])
	}
	// A different day sees none of it.
	other, err := st.AbuseUserCountsForDay("2026-07-19")
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if other[u.ID] != 0 {
		t.Fatalf("day scoping leaked: got %d for an empty day", other[u.ID])
	}
}

// TestAbuseRecentCarriesUserName: the fleet view must join the name so the operator
// acts without a lookup per row.
func TestAbuseRecentCarriesUserName(t *testing.T) {
	st := abuseTestStore(t)
	u, _ := st.CreateUser("alice", "uuid1", "pw", "tok1", 0, 0, 0)
	if err := st.AddAbuseMatches([]AbuseHit{
		{UserID: u.ID, Domain: "evil.example", Category: "malware", Day: "2026-07-20", Count: 1, SeenAt: 100},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	rows, err := st.AbuseRecent(10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(rows) != 1 || rows[0].UserName != "alice" {
		t.Fatalf("want name alice, got %+v", rows)
	}
}

// TestAbuseSurvivesDeletedUser: a match for a user deleted mid-batch must not void
// the rest — the EXISTS guard, same as connections.
func TestAbuseSurvivesDeletedUser(t *testing.T) {
	st := abuseTestStore(t)
	u, _ := st.CreateUser("u1", "uuid1", "pw", "tok1", 0, 0, 0)

	if err := st.AddAbuseMatches([]AbuseHit{
		{UserID: 999999, Domain: "ghost.example", Category: "malware", Day: "2026-07-20", Count: 1, SeenAt: 100},
		{UserID: u.ID, Domain: "real.example", Category: "piracy", Day: "2026-07-20", Count: 1, SeenAt: 100},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	rows, err := st.AbuseByUser(u.ID, 10)
	if err != nil {
		t.Fatalf("by user: %v", err)
	}
	if len(rows) != 1 || rows[0].Domain != "real.example" {
		t.Fatalf("ghost user voided the batch: %+v", rows)
	}
}

func TestPurgeAbuseMatches(t *testing.T) {
	st := abuseTestStore(t)
	u, _ := st.CreateUser("u1", "uuid1", "pw", "tok1", 0, 0, 0)
	if err := st.AddAbuseMatches([]AbuseHit{
		{UserID: u.ID, Domain: "old.example", Category: "malware", Day: "2026-07-01", Count: 1, SeenAt: 100},
		{UserID: u.ID, Domain: "new.example", Category: "malware", Day: "2026-07-20", Count: 1, SeenAt: 200},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	n, err := st.PurgeAbuseMatches("2026-07-10")
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
	rows, _ := st.AbuseByUser(u.ID, 10)
	if len(rows) != 1 || rows[0].Domain != "new.example" {
		t.Fatalf("wrong row survived: %+v", rows)
	}
}
