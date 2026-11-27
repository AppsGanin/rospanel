package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// A minute counts once per user and lane however many connections it opens; the
// flush books it under today, and the breakdown names the lane.
func TestLaneMinutesCountOncePerMinute(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	u, _ := m.CreateUser(adminCtx(), "a", 0, 0)
	at := time.Unix(1_800_000_000, 0)
	for range 50 {
		m.RecordLaneActivity(u.ID, xray.TagVLESS, at)
	}
	m.RecordLaneActivity(u.ID, xray.TagVLESS, at.Add(time.Minute))
	m.RecordLaneActivity(u.ID, xray.TagHysteria, at)
	m.RecordLocalLane(model.UserEmail(u.ID), xray.TagHysteria) // now: a minute of its own
	m.RecordLocalLane("not-a-user", xray.TagHysteria)
	m.FlushUsage()

	today := time.Now().In(m.loc()).Format("2006-01-02")
	rows, err := m.UserLaneBreakdown(u.ID, today, today)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Name] = r.Minutes
	}
	if got[model.ProtoVLESS] != 2 || got[model.ProtoHysteria] != 2 || len(got) != 2 {
		t.Errorf("minutes by protocol: %v", got)
	}
}

// A node's report is untrusted: only users it is believed to serve and lanes it can
// have are booked, each row capped.
func TestNodeLaneMinutesFiltered(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	a, _ := m.CreateUser(adminCtx(), "a", 0, 0)
	b, _ := m.CreateUser(adminCtx(), "b", 0, 0)
	m.ingestNodeLaneMinutes(model.LocalNodeID, []nodeapi.UserLaneSample{
		{UserID: a.ID, Lane: xray.TagVLESS, Minutes: 3},
		{UserID: a.ID, Lane: xray.TagReality, Minutes: 100000},
		{UserID: a.ID, Lane: "made-up", Minutes: 5},
		{UserID: b.ID, Lane: xray.TagVLESS, Minutes: 5},
	}, func(id int64) bool { return id == a.ID })
	m.FlushUsage()

	today := time.Now().In(m.loc()).Format("2006-01-02")
	rows, _ := m.UserLaneBreakdown(a.ID, today, today)
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Lane] = r.Minutes
	}
	if got[xray.TagVLESS] != 3 || got[xray.TagReality] != userLaneReportMax || len(got) != 2 {
		t.Errorf("a: %v", got)
	}
	if rows, _ := m.UserLaneBreakdown(b.ID, today, today); len(rows) != 0 {
		t.Errorf("b was not believed, got %v", rows)
	}
}

// Fetches of one install fold into one row; the shares count users.
func TestSubClientsRecorded(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	u, _ := m.CreateUser(adminCtx(), "a", 0, 0)
	now := time.Now()
	m.RecordSubClient(u.ID, "Happ/3.7.0", "", "iOS", "iPhone15,2", "192.0.2.7", now)
	m.RecordSubClient(u.ID, "Happ/3.7.1", "", "iOS", "", "", now.Add(time.Second))
	m.RecordSubClient(u.ID, "v2rayNG/1.9.30", "hw-1", "", "", "192.0.2.8", now)
	m.FlushUsage()

	list, err := m.UserSubClients(u.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("clients: %v %v", list, err)
	}
	for _, c := range list {
		// The batch keeps the first address the install was seen at, even though the
		// fetch that followed named none.
		if c.App == "Happ" && (c.Version != "3.7.1" || c.Fetches != 2 || c.Model != "iPhone15,2" || c.IP != "192.0.2.7") {
			t.Errorf("happ row: %+v", c)
		}
	}
	day := now.In(m.loc()).Format("2006-01-02")
	apps, platforms, users, err := m.ClientShares(day, day)
	if err != nil || len(apps) != 2 || len(platforms) != 2 || users != 1 {
		t.Errorf("shares: %v %v %d %v", apps, platforms, users, err)
	}
}
