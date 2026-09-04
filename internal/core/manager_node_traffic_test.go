package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	subpkg "github.com/AppsGanin/rospanel/internal/sub"
)

// The cap window is what makes the number mean anything: a monthly allowance
// compared against today's traffic would never trip, and a daily one compared against
// the month would trip on the 2nd.
func TestTrafficPeriodStart(t *testing.T) {
	now := time.Date(2026, 9, 17, 13, 45, 0, 0, time.UTC)
	if got := trafficPeriodStart(model.TrafficDay, now); got != "2026-09-17" {
		t.Errorf("day window starts %q, want today", got)
	}
	for _, p := range []string{model.TrafficMonth, "", "nonsense"} {
		if got := trafficPeriodStart(p, now); got != "2026-09-01" {
			t.Errorf("period %q starts %q, want the 1st", p, got)
		}
	}
}

// Clearing the limit has to clear what hangs off it. A stored "hide when over" with
// no limit to be over would be a switch that does nothing and reads as if it does.
func TestPlacementNormalizeClearsTheCap(t *testing.T) {
	p := model.Placement{TrafficLimit: 0, TrafficPeriod: model.TrafficDay, HideWhenOver: true}.Normalized()
	if p.TrafficPeriod != "" || p.HideWhenOver {
		t.Errorf("cleared limit left period=%q hide=%v", p.TrafficPeriod, p.HideWhenOver)
	}
	// A negative limit is not "unlimited" — it would compare as already exceeded.
	if n := (model.Placement{TrafficLimit: -1}).Normalized(); n.TrafficLimit != 0 {
		t.Errorf("negative limit normalised to %d, want 0", n.TrafficLimit)
	}
}

func TestOverTrafficLimit(t *testing.T) {
	uncapped := model.Placement{}
	if uncapped.OverTrafficLimit(1 << 60) {
		t.Error("a server with no cap read as over it")
	}
	capped := model.Placement{TrafficLimit: 100}
	for _, c := range []struct {
		used int64
		want bool
	}{{99, false}, {100, true}, {101, true}} {
		if got := capped.OverTrafficLimit(c.used); got != c.want {
			t.Errorf("used %d: over=%v, want %v", c.used, got, c.want)
		}
	}
}

// The end-to-end shape: traffic recorded against a server, a cap below it, and the
// panel reports the server as over — for the master (node 0, whose placement lives in
// settings) as well as for a node.
func TestRefreshNodeTrafficMarksServersOver(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "cap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st}

	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	today := time.Now().In(m.loc()).Format("2006-01-02")
	if err := st.AddDailyTrafficNode(u.ID, model.LocalNodeID, today, 6<<30, 6<<30); err != nil {
		t.Fatalf("traffic: %v", err)
	}

	// No cap yet: usage is reported, nobody is over.
	m.refreshNodeTraffic()
	if got := m.NodeTrafficUsage(model.LocalNodeID); got.Used != 12<<30 || got.Over {
		t.Fatalf("uncapped usage = %+v, want 12 GiB and not over", got)
	}
	if len(m.ServersOverTrafficLimit()) != 0 {
		t.Error("a server with no cap was reported over it")
	}

	// A cap under what has been carried, and the master is over — but only hidden
	// once the operator asks for that.
	set, _ := st.GetSettings()
	set.MasterPlacement = model.Placement{TrafficLimit: 10 << 30}
	if err := st.SetMasterPlacement(set.MasterPlacement); err != nil {
		t.Fatalf("placement: %v", err)
	}
	m.refreshNodeTraffic()
	if got := m.NodeTrafficUsage(model.LocalNodeID); !got.Over {
		t.Fatalf("over the cap but usage = %+v", got)
	}
	if !m.ServersOverTrafficLimit()[model.LocalNodeID] {
		t.Fatalf("over the cap but absent from the over-limit set")
	}

	// Being over is a fact; hiding is the server's own policy, and sub.Order is what
	// joins the two — so the set says "over" either way.
	srv := []subpkg.Server{{Set: &model.Settings{ServerID: model.LocalNodeID,
		ServerPlacement: model.Placement{TrafficLimit: 10 << 30}}}}
	if got := subpkg.Order(srv, model.OrderManual, "", nil, m.ServersOverTrafficLimit()); len(got) != 1 {
		t.Error("a server over its cap was hidden without hide_when_over")
	}
	srv[0].Set.ServerPlacement.HideWhenOver = true
	if got := subpkg.Order(srv, model.OrderManual, "", nil, m.ServersOverTrafficLimit()); len(got) != 1 {
		// Never empty: the one-server case is exactly the "never empty the list" rule.
		t.Error("the last server was dropped, emptying the subscription")
	}
	two := []subpkg.Server{srv[0], {Set: &model.Settings{ServerID: 7}}}
	got := subpkg.Order(two, model.OrderManual, "", nil, m.ServersOverTrafficLimit())
	if len(got) != 1 || got[0].Set.ServerID != 7 {
		t.Errorf("ordering kept %d servers, want only the one with allowance left", len(got))
	}

	// Raising the cap above what was carried puts it back.
	if err := st.SetMasterPlacement(model.Placement{TrafficLimit: 100 << 30, HideWhenOver: true}); err != nil {
		t.Fatalf("placement: %v", err)
	}
	m.refreshNodeTraffic()
	if m.ServersOverTrafficLimit()[model.LocalNodeID] {
		t.Error("a raised cap did not bring the server back")
	}
}
