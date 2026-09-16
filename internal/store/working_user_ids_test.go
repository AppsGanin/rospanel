package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// WorkingUserIDs answers the access flush's question — who belongs in the proxy
// config — without the full read. It is only safe if it names exactly the users
// WorkingUsers does, so the two are compared across every way a user can drop out.
func TestWorkingUserIDsMatchWorkingUsers(t *testing.T) {
	st := newStore(t)
	now := time.Now().Unix()
	mk := func(name string, dataLimit, expireAt int64, deviceLimit int) int64 {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, dataLimit, expireAt, deviceLimit)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	plain := mk("plain", 0, 0, 0)
	mk("dated", 0, now+3600, 0)
	mk("expired", 0, now-3600, 0)
	over := mk("over-quota", 1000, 0, 0)
	if err := st.UpdateTraffic(over, 600, 600, 0, 0); err != nil {
		t.Fatal(err)
	}
	off := mk("disabled", 0, 0, 0)
	if err := st.SetUserEnabled(off, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserOnHold("held", "uuid-held", "pw", "tok-held", 0, 86400); err != nil {
		t.Fatal(err)
	}
	// Keys on a working user and on one who is not, so the credentials read is held to
	// the working user's key and cannot pass by leaving every key blank.
	for _, id := range []int64{plain, off} {
		if _, err := st.ClaimUsersAWG([]AWGClaim{{UserID: id, Key: fmt.Sprintf("wg-key-of-%d", id)}}, 2, 65534); err != nil {
			t.Fatal(err)
		}
	}
	crowded := mk("device-limited", 0, 0, 1)
	for _, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		if err := st.AddConnection(crowded, ip, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.Exec(`UPDATE users SET device_over_since = ? WHERE id = ?`, now-3600, crowded); err != nil {
		t.Fatal(err)
	}

	full, err := st.WorkingUsers(now)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := st.WorkingUserIDs(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != len(ids) {
		t.Fatalf("WorkingUsers has %d users, WorkingUserIDs %d ids", len(full), len(ids))
	}
	for i, u := range full {
		if ids[i] != u.ID {
			t.Errorf("position %d: WorkingUsers says %d (%s), WorkingUserIDs says %d", i, u.ID, u.Name, ids[i])
		}
	}
	// And the set is the one expected, so the comparison is not two empty lists.
	if len(ids) != 3 {
		t.Errorf("working ids %v — want plain, dated and held", ids)
	}

	// WorkingCredentials names the same users in the same order, carrying exactly the
	// credentials the full read decrypts and nothing else.
	creds, err := st.WorkingCredentials(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != len(full) {
		t.Fatalf("WorkingUsers has %d users, WorkingCredentials %d", len(full), len(creds))
	}
	for i, u := range full {
		want := model.User{ID: u.ID, UUID: u.UUID, Password: u.Password, WGPrivateKey: u.WGPrivateKey, AWGSlot: u.AWGSlot}
		if !reflect.DeepEqual(creds[i], want) {
			t.Errorf("position %d: WorkingCredentials %+v, want %+v", i, creds[i], want)
		}
	}
	if creds[0].ID != plain || creds[0].Password != "pw" || creds[0].WGPrivateKey != fmt.Sprintf("wg-key-of-%d", plain) {
		t.Errorf("first working user's credentials came back as %+v", creds[0])
	}
}

// WorkingSet stands in for two reads at once, so it has to answer exactly what both
// answer — including where they disagree: a user over their device limit leaves the
// config but keeps their speed cap.
func TestWorkingSetMatchesTheReadsItReplaces(t *testing.T) {
	st := newStore(t)
	now := time.Now().Unix()
	mk := func(name string, kbps int, dataLimit, expireAt int64, deviceLimit int) int64 {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, dataLimit, expireAt, deviceLimit)
		if err != nil {
			t.Fatal(err)
		}
		if kbps > 0 {
			if err := st.SetUserSpeedLimit(u.ID, kbps); err != nil {
				t.Fatal(err)
			}
		}
		return u.ID
	}
	plain := mk("plain", 0, 0, 0, 0)
	capped := mk("capped", 4000, 0, 0, 0)
	mk("expired", 5000, 0, now-3600, 0)
	quota := mk("over-quota", 6000, 1000, 0, 0)
	if err := st.UpdateTraffic(quota, 600, 600, 0, 0); err != nil {
		t.Fatal(err)
	}
	off := mk("disabled", 7000, 0, 0, 0)
	if err := st.SetUserEnabled(off, false); err != nil {
		t.Fatal(err)
	}
	// A group's cap reaches the same answer as the user's own column.
	grouped := mk("grouped", 0, 0, 0, 0)
	g, err := st.CreateGroup("fast", nil, 9000)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserGroups(grouped, []int64{g.ID}); err != nil {
		t.Fatal(err)
	}
	// Over their device limit, past the grace: out of the config, still shaped.
	crowded := mk("device-limited", 3000, 0, 0, 1)
	for _, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		if err := st.AddConnection(crowded, ip, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.Exec(`UPDATE users SET device_over_since = ? WHERE id = ?`, now-3600, crowded); err != nil {
		t.Fatal(err)
	}

	ids, caps, err := st.WorkingSet(now)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs, err := st.WorkingUserIDs(now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Errorf("WorkingSet ids %v, WorkingUserIDs %v", ids, wantIDs)
	}
	wantCaps, err := st.CappedUsers(now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(caps, wantCaps) {
		t.Errorf("WorkingSet caps %v, CappedUsers %v", caps, wantCaps)
	}
	// And both are the sets expected, so this is not two pairs of empty answers.
	if !reflect.DeepEqual(ids, []int64{plain, capped, grouped}) {
		t.Errorf("working ids %v — want plain, capped and grouped", ids)
	}
	if !reflect.DeepEqual(caps, map[int64]int{capped: 4000, grouped: 9000, crowded: 3000}) {
		t.Errorf("caps %v — want the capped, grouped and device-limited users", caps)
	}
}
