package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A count for chosen users is the window count restricted to them: an address seen
// before the window does not count, and a user with nothing in it is simply absent.
func TestActiveDeviceCountsOfMatchesTheWindow(t *testing.T) {
	st := newStore(t)
	now := time.Now().Unix()
	var ids []int64
	for _, name := range []string{"a", "b", "c"} {
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	seen := map[int64][]int64{
		ids[0]: {now, now - 10, now - model.DeviceOnlineWindow - 60},
		ids[1]: {now - model.DeviceOnlineWindow - 60},
		ids[2]: {now},
	}
	for id, times := range seen {
		for i, at := range times {
			if err := st.AddConnection(id, "198.51.100."+string(rune('1'+i)), at); err != nil {
				t.Fatal(err)
			}
		}
	}
	since := now - model.DeviceOnlineWindow
	all, err := st.ActiveDeviceCounts(since)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.ActiveDeviceCountsOf(ids[:2], since)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int64]int{ids[0]: 2}; !reflect.DeepEqual(got, want) || all[ids[0]] != 2 || all[ids[2]] != 1 {
		t.Fatalf("counts of a and b %v, want %v (window counts %v)", got, want, all)
	}
	if got, err := st.ActiveDeviceCountsOf(nil, since); err != nil || len(got) != 0 {
		t.Fatalf("no users: %v %v", got, err)
	}
}

// A user state carries each field it names exactly as ListUsers does, and leaves the
// rest zero — credentials above all. Both ways of counting devices are held to it: by
// the users' own rows, and by the grouped window used past userStatesByKeyMax.
func TestUserStatesMatchWholeUsers(t *testing.T) {
	st := newStore(t)
	now := time.Now().Unix()
	a, err := st.CreateUser("a", "uuid-a", "pw-a", "tok-a", 1000, now+86400, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateTraffic(a.ID, 300, 400, 7000, 8000); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTelegramChat(a.ID, 4242); err != nil {
		t.Fatal(err)
	}
	if err := st.SetResetPeriod(a.ID, "monthly", now-3600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNotifiedStatus(a.ID, model.StatusActive); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNotifiedExpireAt(a.ID, now+86400); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNotifiedQuotaAt(a.ID, now-60); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		if err := st.AddConnection(a.ID, ip, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.StampDeviceOverLimit(now - model.DeviceLimitGrace - 10); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE users SET plan_id = 9, hold_seconds = 3600 WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("b", "uuid-b", "pw-b", "tok-b", 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	whole, err := st.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(whole))
	for i, u := range whole {
		ids[i] = u.ID
	}
	byKey, err := st.UserStatesByID(ids)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ids)
	byWindow, err := st.userStates(`SELECT `+userStateCols+` FROM users
		WHERE id IN (SELECT value FROM json_each(?)) ORDER BY id DESC`, false, string(b))
	if err != nil {
		t.Fatal(err)
	}
	for _, states := range [][]model.User{byKey, byWindow} {
		if len(whole) != len(states) {
			t.Fatalf("%d users, %d states", len(whole), len(states))
		}
		compareStates(t, whole, states)
	}
	// The fixture sets what it compares.
	if got := whole[1]; got.LastUp != 7000 || got.TgChatID != 4242 || got.PlanID != 9 || got.ActiveDevices != 2 ||
		got.Status != model.StatusDeviceLimited || got.NotifiedQuotaAt == 0 || got.HoldSeconds != 3600 {
		t.Fatalf("fixture not as intended: %+v", got)
	}
}

func compareStates(t *testing.T, whole, states []model.User) {
	t.Helper()
	for i, u := range whole {
		want := model.User{
			ID: u.ID, Name: u.Name, Enabled: u.Enabled, PlanID: u.PlanID, DataLimit: u.DataLimit,
			ExpireAt: u.ExpireAt, HoldSeconds: u.HoldSeconds, UsedUp: u.UsedUp, UsedDown: u.UsedDown,
			LastUp: u.LastUp, LastDown: u.LastDown, ResetPeriod: u.ResetPeriod, LastResetAt: u.LastResetAt,
			DeviceLimit: u.DeviceLimit, DeviceOverSince: u.DeviceOverSince, TgChatID: u.TgChatID,
			NotifiedStatus: u.NotifiedStatus, NotifiedExpireAt: u.NotifiedExpireAt, NotifiedQuotaAt: u.NotifiedQuotaAt,
			ActiveDevices: u.ActiveDevices, Status: u.Status,
		}
		if !reflect.DeepEqual(states[i], want) {
			t.Fatalf("user %d:\n state %+v\n want  %+v", u.ID, states[i], want)
		}
	}
}

// The tunnel poll gets every user's key that exists, decrypted.
func TestUserTunnelKeys(t *testing.T) {
	st := newStore(t)
	a, _ := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0)
	b, _ := st.CreateUser("b", "uuid-b", "pw", "tok-b", 0, 0, 0)
	if _, err := st.ClaimUsersAWG([]AWGClaim{{UserID: a.ID, Key: "key-of-a"}}, 2, 65534); err != nil {
		t.Fatal(err)
	}
	keys, err := st.UserTunnelKeys()
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int64]string{a.ID: "key-of-a"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys %v, want %v (b=%d has none)", keys, want, b.ID)
	}
}

// A few rows read by id are exactly those rows of the whole list — statuses included,
// the one a device count decides among them — newest first, with an id that has no
// user left out. The users page brings its shared list up to date with them.
func TestUserSummariesOfAreThoseRowsOfTheList(t *testing.T) {
	st := newStore(t)
	now := time.Now().Unix()
	mk := func(name string, limit, expire int64, devices int) int64 {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, limit, expire, devices)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	active := mk("active", 0, 0, 0)
	if err := st.SetUserTags(active, []string{"vip"}); err != nil {
		t.Fatal(err)
	}
	off := mk("off", 0, 0, 0)
	if err := st.SetUserEnabled(off, false); err != nil {
		t.Fatal(err)
	}
	expired := mk("expired", 0, now-3600, 0)
	quota := mk("quota", 1000, 0, 0)
	if err := st.UpdateTraffic(quota, 600, 600, 0, 0); err != nil {
		t.Fatal(err)
	}
	crowd := mk("crowd", 0, 0, 1)
	for _, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		if err := st.AddConnection(crowd, ip, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.StampDeviceOverLimit(now - model.DeviceLimitGrace - 10); err != nil {
		t.Fatal(err)
	}
	mk("not asked for", 0, 0, 0)

	all, err := st.ListUserSummaries()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]UserSummary{}
	for _, u := range all {
		byID[u.ID] = u
	}
	asked := []int64{active, crowd, 99999, expired, off, quota}
	got, err := st.ListUserSummariesOf(asked)
	if err != nil {
		t.Fatal(err)
	}
	want := []UserSummary{byID[crowd], byID[quota], byID[expired], byID[off], byID[active]} // newest first
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows by id:\n got  %+v\n want %+v", got, want)
	}
	statuses := map[string]bool{}
	for _, u := range got {
		statuses[u.Status] = true
	}
	for _, s := range []string{model.StatusActive, model.StatusDisabled, model.StatusExpired, model.StatusLimited, model.StatusDeviceLimited} {
		if !statuses[s] {
			t.Errorf("the fixture has no %q row, so that status was not compared: %v", s, statuses)
		}
	}
	if none, err := st.ListUserSummariesOf(nil); err != nil || len(none) != 0 {
		t.Errorf("no ids: %v %v", none, err)
	}
}
