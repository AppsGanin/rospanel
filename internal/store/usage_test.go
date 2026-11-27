package store

import (
	"fmt"
	"testing"
)

func TestSubClientsUpsertTrimAndShares(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	a, err := s.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateUser("b", "uuid-b", "pw", "tok-b", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSubClients([]SubClient{
		{UserID: a.ID, Key: "a:Happ|iOS", App: "Happ", Version: "3.6", Platform: "iOS", Model: "iPhone", IP: "10.0.0.1", FirstSeen: 100, LastSeen: 100, Fetches: 2},
		{UserID: b.ID, Key: "a:Happ|Android", App: "Happ", Platform: "Android", FirstSeen: 100, LastSeen: 100, Fetches: 1},
		{UserID: 9999, Key: "a:x|", App: "x", FirstSeen: 100, LastSeen: 100}, // no such user: skipped
	}); err != nil {
		t.Fatal(err)
	}
	// A later fetch updates the version and keeps the model it does not name; a fetch
	// that names no address keeps the one the row already has.
	if err := s.RecordSubClients([]SubClient{
		{UserID: a.ID, Key: "a:Happ|iOS", App: "Happ", Version: "3.7", Platform: "iOS", FirstSeen: 200, LastSeen: 200, Fetches: 1},
	}); err != nil {
		t.Fatal(err)
	}
	list, err := s.UserSubClients(a.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if c := list[0]; c.Version != "3.7" || c.Model != "iPhone" || c.IP != "10.0.0.1" || c.Fetches != 3 || c.FirstSeen != 100 || c.LastSeen != 200 {
		t.Errorf("merged row: %+v", c)
	}
	// The row names itself: the card uses it as the install's id, so a read that
	// forgot the key would hand every row the same empty one.
	if list[0].Key != "a:Happ|iOS" {
		t.Errorf("row key: %q", list[0].Key)
	}
	// A fetch from another address moves the row: the card groups by where the
	// subscription was last refreshed.
	if err := s.RecordSubClients([]SubClient{
		{UserID: a.ID, Key: "a:Happ|iOS", App: "Happ", Platform: "iOS", IP: "10.0.0.2", FirstSeen: 300, LastSeen: 300, Fetches: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.UserSubClients(a.ID); len(list) != 1 || list[0].IP != "10.0.0.2" {
		t.Errorf("address not moved: %+v", list)
	}

	apps, platforms, users, err := s.ClientShares(0, 1<<62)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0] != (ClientShare{Name: "Happ", Users: 2}) || len(platforms) != 2 || users != 2 {
		t.Errorf("shares: %v %v %d", apps, platforms, users)
	}
	if apps, _, users, _ := s.ClientShares(150, 1<<62); len(apps) != 1 || apps[0].Users != 1 || users != 1 {
		t.Errorf("since 150: %v %d", apps, users)
	}
	// The end of the range is exclusive: a fetch at 200 is the only one inside [150,201).
	if apps, _, users, _ := s.ClientShares(0, 200); len(apps) != 1 || users != 1 || apps[0].Users != 1 {
		t.Errorf("before 200: %v %d", apps, users)
	}

	// One user cannot grow the table past SubClientsPerUser: the oldest rows go.
	var many []SubClient
	for i := range SubClientsPerUser + 5 {
		many = append(many, SubClient{UserID: b.ID, Key: fmt.Sprintf("h:%d", i), App: "spam",
			FirstSeen: int64(1000 + i), LastSeen: int64(1000 + i)})
	}
	if err := s.RecordSubClients(many); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.UserSubClients(b.ID); len(list) != SubClientsPerUser || list[len(list)-1].LastSeen != 1005 {
		t.Errorf("trimmed to %d rows, oldest %d", len(list), list[len(list)-1].LastSeen)
	}

	if n, err := s.PurgeSubClients(1010); err != nil || n != 5+1 { // b's 1005..1009, a's 200
		t.Errorf("purge: %d %v", n, err)
	}
}

func TestLaneMinutes(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	a, err := s.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddLaneMinutes([]LaneMinutes{
		{UserID: a.ID, Day: "2026-11-20", Lane: "vless-in", Minutes: 30},
		{UserID: a.ID, Day: "2026-11-21", Lane: "vless-in", Minutes: 10},
		{UserID: a.ID, Day: "2026-11-21", Lane: "awg", Minutes: 5000}, // capped to a day
		{UserID: 9999, Day: "2026-11-21", Lane: "awg", Minutes: 5},   // no such user
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLaneMinutes([]LaneMinutes{{UserID: a.ID, Day: "2026-11-21", Lane: "awg", Minutes: 5}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.UserLaneUsage(a.ID, "2026-11-20", "2026-11-21")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]LaneUsage{}
	for _, r := range rows {
		got[r.Lane] = r
	}
	if v := got["vless-in"]; v.Minutes != 40 || v.Days != 2 || v.LastDay != "2026-11-21" {
		t.Errorf("vless: %+v", v)
	}
	if v := got["awg"]; v.Minutes != 1440 {
		t.Errorf("awg: %+v", v)
	}
	if _, err := s.PurgeTrafficDaily("2026-11-21"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.UserLaneUsage(a.ID, "2026-11-20", "2026-11-20"); len(rows) != 0 {
		t.Errorf("purged day still there: %v", rows)
	}
}
