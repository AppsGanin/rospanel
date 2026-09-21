package core

import (
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The online gauge counts distinct users per server inside the online window,
// forgets them once they age out, and attributes a sighting to the server it was
// reported from — the master's own log to 0, a node's report to the node.
func TestOnlineGaugeCountsPerServer(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	a, _ := m.CreateUser(ctx, "a", 0, 0)
	b, _ := m.CreateUser(ctx, "b", 0, 0)

	m.RecordLocalAccess(model.UserEmail(a.ID), "198.51.100.1", "")
	m.RecordLocalAccess(model.UserEmail(a.ID), "198.51.100.2", "") // same user, second address: still one
	m.RecordAccessOn(7, model.UserEmail(a.ID), "198.51.100.3", "")
	m.RecordAccessOn(7, model.UserEmail(b.ID), "198.51.100.4", "")
	m.RecordAccessOn(7, "not-a-user", "198.51.100.5", "") // ignored

	got := m.OnlineByServer()
	if got[model.LocalNodeID] != 1 || got[7] != 2 {
		t.Errorf("online by server: %v, want {0:1 7:2}", got)
	}

	// Age out: a sighting older than the window is gone, and so is the server key.
	// Asked as of a moment the last count no longer answers for, so it is counted.
	m.online.record(9, a.ID, time.Now().Unix()-model.DeviceOnlineWindow-1)
	got = m.online.recent(time.Now().Add(onlineCountsAge))
	if _, ok := got[9]; ok {
		t.Errorf("stale sighting counted: %v", got)
	}
	if got[7] != 2 {
		t.Errorf("live sightings lost: %v", got)
	}
}

// Every subscription fetch asks for the count, so one count answers for a couple of
// seconds — and no longer, and not for a clock that stepped back, and never as a map
// a caller could change for the next one.
func TestOnlineCountIsReusedBriefly(t *testing.T) {
	t.Parallel()
	var g onlineGauge
	t0 := time.Now()
	g.record(7, 1, t0.Unix())
	if got := g.recent(t0); got[7] != 1 {
		t.Fatalf("first count: %v", got)
	}
	g.record(7, 2, t0.Unix())
	if got := g.recent(t0.Add(onlineCountsAge / 2)); got[7] != 1 {
		t.Errorf("a count younger than its age was not reused: %v", got)
	}
	if got := g.recent(t0.Add(onlineCountsAge)); got[7] != 2 {
		t.Errorf("a count as old as its age was reused: %v", got)
	}
	g.record(7, 3, t0.Unix())
	if got := g.recent(t0.Add(-time.Second)); got[7] != 3 {
		t.Errorf("a clock that stepped back kept the last count: %v", got)
	}
	mine := g.recent(t0)
	mine[7] = 99
	if got := g.recent(t0); got[7] != 3 {
		t.Errorf("a caller's change reached the next caller: %v", got)
	}
}

func TestMasterPlacementValidatesAndNormalises(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	if err := m.SetMasterPlacement(model.Placement{Country: "xyz"}); err == nil {
		t.Error("a three-letter country was accepted")
	}
	if err := m.SetMasterPlacement(model.Placement{Capacity: -1}); err == nil {
		t.Error("a negative capacity was accepted")
	}
	if err := m.SetMasterPlacement(model.Placement{Country: " nl ", Weight: 3, Capacity: 50, HideWhenFull: true}); err != nil {
		t.Fatal(err)
	}
	set, err := m.store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if p := set.MasterPlacement; p.Country != "NL" || p.Weight != 3 || p.Capacity != 50 || !p.HideWhenFull {
		t.Errorf("stored placement: %+v", p)
	}
	// The mode survives a settings save and an unknown one is refused.
	set.SubOrderMode = model.OrderNearestLoad
	if err := m.SaveSubSettings(set); err != nil {
		t.Fatal(err)
	}
	if again, _ := m.store.GetSettings(); again.SubOrderMode != model.OrderNearestLoad {
		t.Errorf("order mode not stored: %q", again.SubOrderMode)
	}
	set.SubOrderMode = "fastest"
	if err := m.SaveSubSettings(set); err == nil {
		t.Error("an unknown order mode was accepted")
	}
}

// Sightings recorded from every node's report while subscriptions ask for the counts.
func TestOnlineCountUnderConcurrency(t *testing.T) {
	t.Parallel()
	var g onlineGauge
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 2000 {
				if i%2 == 0 {
					g.record(int64(i), int64(j), time.Now().Unix())
					continue
				}
				counts := g.recent(time.Now().Add(time.Duration(j) * time.Millisecond))
				counts[99] = j // a caller's own map
			}
		}()
	}
	wg.Wait()
	got := g.recent(time.Now().Add(onlineCountsAge * 2))
	for _, server := range []int64{0, 2, 4} {
		if got[server] != 2000 {
			t.Errorf("server %d: %d users, want 2000", server, got[server])
		}
	}
	if _, ok := got[99]; ok {
		t.Error("a caller's change reached the gauge")
	}
}
