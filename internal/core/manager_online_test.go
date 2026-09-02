package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The online gauge counts distinct users per server inside the online window,
// forgets them once they age out, and attributes a sighting to the server it was
// reported from — the master's own log to 0, a node's report to the node.
func TestOnlineGaugeCountsPerServer(t *testing.T) {
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
	m.online.record(9, a.ID, time.Now().Unix()-model.DeviceOnlineWindow-1)
	got = m.OnlineByServer()
	if _, ok := got[9]; ok {
		t.Errorf("stale sighting counted: %v", got)
	}
	if got[7] != 2 {
		t.Errorf("live sightings lost: %v", got)
	}
}

func TestMasterPlacementValidatesAndNormalises(t *testing.T) {
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
	set.SubOrderMode = "random"
	if err := m.SaveSubSettings(set); err == nil {
		t.Error("an unknown order mode was accepted")
	}
}
