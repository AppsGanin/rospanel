package store

import (
	"testing"
	"time"
)

// Two ways into a user's quota cycle were missing the anchor that every other way
// sets, and both fail silently: nothing errors, the numbers just stop meaning what
// the operator was shown.

// A bulk reset must restart a rolling cycle exactly as a single reset does. Only the
// single path moved the anchor, so resetting a whole selection the day before their
// cycle rolled gave each of them a quota that expired the next morning.
func TestBulkResetRestartsTheQuotaCycle(t *testing.T) {
	st := openTestStore(t)
	rolling, err := st.CreateUser("rolling", "11111111-1111-1111-1111-111111111111", "p", "tok-rolling", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := st.CreateUser("fixed", "22222222-2222-2222-2222-222222222222", "p", "tok-fixed", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-29 * 24 * time.Hour).Unix()
	if err := st.SetResetPeriod(rolling.ID, "monthly", old); err != nil {
		t.Fatal(err)
	}
	if err := st.SetResetPeriod(fixed.ID, "none", old); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	if _, err := st.ResetTrafficMany(map[int64][2]int64{rolling.ID: {0, 0}, fixed.ID: {0, 0}}, now); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.GetUser(rolling.ID); u.LastResetAt != now {
		t.Errorf("a bulk reset left a monthly user's cycle anchored at %d, want now (%d)", u.LastResetAt, now)
	}
	// A user with no period has no cycle to restart, and its anchor is not ours to move.
	if u, _ := st.GetUser(fixed.ID); u.LastResetAt != old {
		t.Errorf("a bulk reset moved the anchor of a user with no reset period: %d, want %d", u.LastResetAt, old)
	}
}

// An imported user with a reset period has to be able to roll over. With no anchor,
// resetDue answers "not due" forever and the quota never refills.
func TestAnImportedUserWithAPeriodHasACycleToRollFrom(t *testing.T) {
	st := openTestStore(t)
	before := time.Now().Unix()
	monthly, err := st.ImportUser(ImportedUser{
		Name: "monthly", UUID: "33333333-3333-3333-3333-333333333333", Password: "p",
		SubToken: "tok-monthly", ResetPeriod: "monthly", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if monthly.LastResetAt < before {
		t.Errorf("an imported monthly user has anchor %d — a cycle that can never roll over", monthly.LastResetAt)
	}

	none, err := st.ImportUser(ImportedUser{
		Name: "none", UUID: "44444444-4444-4444-4444-444444444444", Password: "p",
		SubToken: "tok-none", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if none.LastResetAt != 0 {
		t.Errorf("an imported user with no period got an anchor (%d) it has no use for", none.LastResetAt)
	}
}
