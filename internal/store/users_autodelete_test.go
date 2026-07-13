package store

import (
	"path/filepath"
	"testing"
	"time"
)

// ExpiredUsersBefore drives the irreversible auto-delete sweep, so its selection
// has to be exactly right: it keys off the expiry DATE (not the derived status),
// never touches users with no expiry, and honours the cutoff so the grace period
// means what it says.
func TestExpiredUsersBefore(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "autodelete.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	now := time.Now().Unix()
	day := int64(86400)

	// Expired 40 days ago — past a 30-day grace period.
	longGone, _ := st.CreateUser("longGone", "u1", "pw", "t1", 0, now-40*day, 0)
	// Expired 10 days ago — inside a 30-day grace period.
	recent, _ := st.CreateUser("recent", "u2", "pw", "t2", 0, now-10*day, 0)
	// No expiry at all — must never be a candidate.
	forever, _ := st.CreateUser("forever", "u3", "pw", "t3", 0, 0, 0)
	// Expires in the future — active, must never be a candidate.
	future, _ := st.CreateUser("future", "u4", "pw", "t4", 0, now+10*day, 0)

	cutoff := now - 30*day // delete anyone who expired more than 30 days ago
	got, err := st.ExpiredUsersBefore(cutoff)
	if err != nil {
		t.Fatalf("ExpiredUsersBefore: %v", err)
	}

	ids := map[int64]bool{}
	for _, u := range got {
		ids[u.ID] = true
	}
	if !ids[longGone.ID] {
		t.Errorf("longGone (expired 40d ago) should be selected")
	}
	if ids[recent.ID] {
		t.Errorf("recent (expired 10d ago, inside 30d grace) must NOT be selected")
	}
	if ids[forever.ID] {
		t.Errorf("forever (no expiry) must NEVER be selected")
	}
	if ids[future.ID] {
		t.Errorf("future (not yet expired) must NEVER be selected")
	}
	if len(got) != 1 {
		t.Errorf("want exactly 1 candidate, got %d", len(got))
	}
}
