package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func slotOf(t *testing.T, st *Store, id int64) int {
	t.Helper()
	u, err := st.GetUser(id)
	if err != nil {
		t.Fatalf("get %d: %v", id, err)
	}
	return u.AWGSlot
}

// A tunnel slot is handed out, not derived from the id: the lowest free one, kept
// once held, freed with the user, and none at all once the subnet is full — whatever
// the user's id.
func TestClaimUsersAWGHandsOutTheLowestFreeSlot(t *testing.T) {
	st := newStore(t)
	mk := func(name string) int64 {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	a, b, c := mk("a"), mk("b"), mk("c")
	// An id the old id-derived address could not hold.
	if _, err := st.db.Exec(`INSERT INTO users (id, name, uuid, password, sub_token) VALUES (70000, 'late', 'uuid-late', 'pw', 'tok-late')`); err != nil {
		t.Fatal(err)
	}
	const late = 70000

	got, err := st.ClaimUsersAWG([]AWGClaim{{a, "key-a"}, {late, "key-late"}, {b, "key-b"}}, 2, 65534)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]AWGIdentity{a: {"key-a", 2}, late: {"key-late", 3}, b: {"key-b", 4}}
	for id, w := range want {
		if got[id] != w || slotOf(t, st, id) != w.Slot {
			t.Errorf("user %d: claimed %+v, stored slot %d — want %+v", id, got[id], slotOf(t, st, id), w)
		}
	}

	// A second claim keeps what the user already has: key and slot both.
	again, err := st.ClaimUsersAWG([]AWGClaim{{a, "another-key"}}, 2, 65534)
	if err != nil {
		t.Fatal(err)
	}
	if again[a] != (AWGIdentity{"key-a", 2}) {
		t.Fatalf("a second claim changed the identity: %+v", again[a])
	}

	// A deleted user's slot is the next one handed out.
	if err := st.DeleteUser(late); err != nil {
		t.Fatal(err)
	}
	reused, err := st.ClaimUsersAWG([]AWGClaim{{c, "key-c"}, {late, "gone"}}, 2, 65534)
	if err != nil {
		t.Fatal(err)
	}
	if reused[c].Slot != 3 {
		t.Fatalf("the freed slot was not reused: c got %d", reused[c].Slot)
	}
	if _, ok := reused[late]; ok {
		t.Fatal("a deleted user came back with an identity")
	}

	// A full subnet: the key is still kept, the slot is none.
	d := mk("d")
	full, err := st.ClaimUsersAWG([]AWGClaim{{d, "key-d"}}, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if full[d] != (AWGIdentity{"key-d", 0}) || slotOf(t, st, d) != 0 {
		t.Fatalf("a claim past the last slot: %+v, stored slot %d", full[d], slotOf(t, st, d))
	}
	// And room again once a slot frees up.
	if err := st.DeleteUser(b); err != nil {
		t.Fatal(err)
	}
	if room, _ := st.ClaimUsersAWG([]AWGClaim{{d, "ignored"}}, 2, 4); room[d] != (AWGIdentity{"key-d", 4}) {
		t.Fatalf("a freed slot did not reach a user waiting for one: %+v", room[d])
	}
}

// Users who already hold a tunnel config keep its address across the upgrade — slot
// id + 1, what the address used to be derived from — and only they get a slot from
// the migration: no key means no config was ever handed out, and an id past the
// subnet never had a working address to keep.
func TestAWGSlotMigrationKeepsHandedOutAddresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-slot.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL DEFAULT (unixepoch()))`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && e.Name() < "0079" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for _, name := range files {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}
	users := []struct {
		id   int64
		key  string
		want int
	}{
		{1, "k1", 2},
		{5, "k5", 6},
		{9, "", 0},
		{65533, "k65533", 65534},
		{65534, "k65534", 0},
		{70000, "k70000", 0},
	}
	for _, u := range users {
		if _, err := db.Exec(`INSERT INTO users (id, name, uuid, password, sub_token, wg_private_key) VALUES (?, ?, ?, 'pw', ?, ?)`,
			u.id, fmt.Sprintf("u%d", u.id), fmt.Sprintf("uuid-%d", u.id), fmt.Sprintf("tok-%d", u.id), u.key); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	for _, u := range users {
		if got := slotOf(t, st, u.id); got != u.want {
			t.Errorf("user %d: slot %d after the upgrade, want %d", u.id, got, u.want)
		}
	}
	// The user past the old subnet gets the lowest slot nobody kept.
	got, err := st.ClaimUsersAWG([]AWGClaim{{70000, "unused"}}, 2, 65534)
	if err != nil {
		t.Fatal(err)
	}
	if got[70000] != (AWGIdentity{"k70000", 3}) {
		t.Fatalf("user 70000 after the upgrade: %+v, want its key and slot 3", got[70000])
	}
	// Two users on one slot are refused by the schema itself.
	if _, err := st.db.Exec(`UPDATE users SET awg_slot = 2 WHERE id = 5`); err == nil {
		t.Fatal("two users were allowed the same slot")
	}
}
