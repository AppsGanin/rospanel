package core

import (
	"path/filepath"
	"strings"
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

// A server that is over its cap must be reported ONCE, not once a minute. The master
// is the case that breaks: it is never in the node list the outage sweep builds its
// "still live" set from, so anything pruning by that set forgets what admins were
// told and tells them again on the next tick. Counting messages, not flags — a flag
// that is re-set to true by the very alert being repeated looks identical to one that
// was never lost.
func TestNodeTrafficAlertsOncePerCrossing(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "alert.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var msgs []string
	m := &Manager{store: st}
	m.SetAdminNotifier(func(html string) { msgs = append(msgs, html) })

	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	today := time.Now().In(m.loc()).Format("2006-01-02")
	if err := st.AddDailyTrafficNode(u.ID, model.LocalNodeID, today, 6<<30, 6<<30); err != nil {
		t.Fatalf("traffic: %v", err)
	}
	if err := st.SetMasterPlacement(model.Placement{TrafficLimit: 1 << 30}); err != nil {
		t.Fatalf("placement: %v", err)
	}

	// Four ticks of the watch loop, in the order the loop runs them.
	for i := 0; i < 4; i++ {
		m.sweepAlerts(nil, nil, time.Now())
		m.refreshNodeTraffic()
	}
	if len(msgs) != 1 {
		t.Fatalf("the crossing was announced %d times, want once:\n%s", len(msgs), strings.Join(msgs, "\n---\n"))
	}

	// Raising the cap is the all-clear, and it is announced exactly once too.
	if err := st.SetMasterPlacement(model.Placement{TrafficLimit: 100 << 30}); err != nil {
		t.Fatalf("placement: %v", err)
	}
	for i := 0; i < 3; i++ {
		m.sweepAlerts(nil, nil, time.Now())
		m.refreshNodeTraffic()
	}
	if len(msgs) != 2 {
		t.Fatalf("after the all-clear there were %d messages, want 2", len(msgs))
	}
}

// A node that is switched off, or was never installed, must not raise a traffic
// alarm. It is not carrying anything, and the outage sweep deliberately FORGETS its
// alert state each pass — so an alarm here would be re-announced every tick for a
// condition the operator created on purpose.
func TestNodeTrafficNoAlertsForServersThatAreNotServing(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "off.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var msgs []string
	m := &Manager{store: st}
	m.SetAdminNotifier(func(html string) { msgs = append(msgs, html) })

	n, err := st.CreateNode("off-node", "off.example.com", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	today := time.Now().In(m.loc()).Format("2006-01-02")
	if err := st.AddDailyTrafficNode(u.ID, n.ID, today, 6<<30, 6<<30); err != nil {
		t.Fatalf("traffic: %v", err)
	}
	if err := st.UpdateNode(n.ID, store.NodeEdit{
		Name: n.Name, Host: n.Host,
		Placement: model.Placement{TrafficLimit: 1 << 30},
	}); err != nil {
		t.Fatalf("cap: %v", err)
	}
	// Never joined (no token exchange) and switched off: not serving either way.
	if err := st.SetNodeEnabled(n.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	for i := 0; i < 3; i++ {
		m.sweepAlerts(nil, nil, time.Now())
		m.refreshNodeTraffic()
	}
	if len(msgs) != 0 {
		t.Errorf("a node that is not serving raised %d alarms:\n%s", len(msgs), strings.Join(msgs, "\n"))
	}
	// The usage figure is still reported — the server card shows it either way.
	if got := m.NodeTrafficUsage(n.ID); got.Used != 12<<30 {
		t.Errorf("usage for a disabled node = %d, want it still counted", got.Used)
	}
}

// A server name is operator input and the alert goes out as HTML. An unescaped angle
// bracket makes Telegram reject the whole message — the alert is dropped, and the one
// an operator must not silently lose is the one about their bill.
func TestNodeTrafficAlertEscapesTheServerName(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "esc.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var msgs []string
	m := &Manager{store: st}
	m.SetAdminNotifier(func(html string) { msgs = append(msgs, html) })

	if err := st.SetMasterLabel("DE <fast> & cheap"); err != nil {
		t.Fatalf("master label: %v", err)
	}
	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	today := time.Now().In(m.loc()).Format("2006-01-02")
	if err := st.AddDailyTrafficNode(u.ID, model.LocalNodeID, today, 6<<30, 6<<30); err != nil {
		t.Fatalf("traffic: %v", err)
	}
	if err := st.SetMasterPlacement(model.Placement{TrafficLimit: 1 << 30}); err != nil {
		t.Fatalf("placement: %v", err)
	}
	m.refreshNodeTraffic()
	if len(msgs) != 1 {
		t.Fatalf("got %d alerts, want 1", len(msgs))
	}
	if strings.Contains(msgs[0], "<fast>") {
		t.Errorf("the server name went out unescaped:\n%s", msgs[0])
	}
	if !strings.Contains(msgs[0], "&lt;fast&gt;") {
		t.Errorf("the escaped name is not in the message:\n%s", msgs[0])
	}
}

// A failed traffic query must not read as "every server has carried nothing": that
// un-hides every capped server at once and sends an all-clear for an allowance that
// has not come back.
func TestNodeTrafficKeepsFiguresWhenTheQueryFails(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "fail.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var msgs []string
	m := &Manager{store: st}
	m.SetAdminNotifier(func(html string) { msgs = append(msgs, html) })

	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	today := time.Now().In(m.loc()).Format("2006-01-02")
	if err := st.AddDailyTrafficNode(u.ID, model.LocalNodeID, today, 6<<30, 6<<30); err != nil {
		t.Fatalf("traffic: %v", err)
	}
	if err := st.SetMasterPlacement(model.Placement{TrafficLimit: 1 << 30, HideWhenOver: true}); err != nil {
		t.Fatalf("placement: %v", err)
	}
	m.refreshNodeTraffic()
	if !m.ServersOverTrafficLimit()[model.LocalNodeID] {
		t.Fatalf("setup: the master is not over its cap")
	}
	before := len(msgs)

	// Close the store: every query now fails, which is what a broken refresh looks like.
	st.Close()
	m.refreshNodeTraffic()

	if !m.ServersOverTrafficLimit()[model.LocalNodeID] {
		t.Error("a failed refresh un-hid a server that is still over its cap")
	}
	if got := m.NodeTrafficUsage(model.LocalNodeID); got.Used == 0 {
		t.Error("a failed refresh overwrote the figures with zero")
	}
	if len(msgs) != before {
		t.Errorf("a failed refresh sent %d extra messages (a false all-clear)", len(msgs)-before)
	}
}
