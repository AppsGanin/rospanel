package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGetUsersByIDs(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	u1, _ := st.CreateUser("user1", "uuid1", "pw1", "tok1", 100, 1000, 0)
	u2, _ := st.CreateUser("user2", "uuid2", "pw2", "tok2", 200, 2000, 0)
	u3, _ := st.CreateUser("user3", "uuid3", "pw3", "tok3", 300, 3000, 0)

	// Empty query
	empty, err := st.GetUsersByIDs(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("GetUsersByIDs(nil) = %v, %v; want nil, nil", empty, err)
	}

	// Fetch u1 and u3 and non-existent id 999
	users, err := st.GetUsersByIDs([]int64{u1.ID, u3.ID, 999})
	if err != nil {
		t.Fatalf("GetUsersByIDs: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	found := map[int64]string{}
	for _, u := range users {
		found[u.ID] = u.Name
	}
	if found[u1.ID] != "user1" || found[u3.ID] != "user3" {
		t.Errorf("unexpected users returned: %+v", found)
	}
	if _, ok := found[u2.ID]; ok {
		t.Errorf("u2 should not be in result")
	}
}

func TestBulkSetUserExpiry(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	now := time.Now().Unix()
	u1, _ := st.CreateUser("user1", "uuid1", "pw1", "tok1", 0, now+100, 0)
	u2, _ := st.CreateUser("user2", "uuid2", "pw2", "tok2", 0, now+200, 0)

	updates := map[int64]int64{
		u1.ID: now + 5000,
		u2.ID: now + 6000,
	}
	if err := st.BulkSetUserExpiry(updates); err != nil {
		t.Fatalf("BulkSetUserExpiry: %v", err)
	}

	got1, _ := st.GetUser(u1.ID)
	got2, _ := st.GetUser(u2.ID)
	if got1.ExpireAt != now+5000 {
		t.Errorf("u1 expire = %d, want %d", got1.ExpireAt, now+5000)
	}
	if got2.ExpireAt != now+6000 {
		t.Errorf("u2 expire = %d, want %d", got2.ExpireAt, now+6000)
	}
}

func TestBulkResetTraffic(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	u1, _ := st.CreateUser("user1", "uuid1", "pw1", "tok1", 0, 0, 0)
	u2, _ := st.CreateUser("user2", "uuid2", "pw2", "tok2", 0, 0, 0)

	_ = st.UpdateTraffic(u1.ID, 500, 500, 500, 500)
	_ = st.UpdateTraffic(u2.ID, 800, 800, 800, 800)

	resets := map[int64][2]int64{
		u1.ID: {1000, 2000},
		u2.ID: {3000, 4000},
	}
	if err := st.BulkResetTraffic(resets); err != nil {
		t.Fatalf("BulkResetTraffic: %v", err)
	}

	got1, _ := st.GetUser(u1.ID)
	got2, _ := st.GetUser(u2.ID)
	if got1.UsedUp != 0 || got1.UsedDown != 0 || got1.LastUp != 1000 || got1.LastDown != 2000 {
		t.Errorf("u1 traffic = (%d, %d, %d, %d), want (0, 0, 1000, 2000)", got1.UsedUp, got1.UsedDown, got1.LastUp, got1.LastDown)
	}
	if got2.UsedUp != 0 || got2.UsedDown != 0 || got2.LastUp != 3000 || got2.LastDown != 4000 {
		t.Errorf("u2 traffic = (%d, %d, %d, %d), want (0, 0, 3000, 4000)", got2.UsedUp, got2.UsedDown, got2.LastUp, got2.LastDown)
	}
}
