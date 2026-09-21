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
	t.Parallel()
	now := time.Date(2026, 9, 17, 13, 45, 0, 0, time.UTC)
	if got := trafficPeriodStart(model.Placement{TrafficPeriod: model.TrafficDay, TrafficResetDay: 14}, now); got != "2026-09-17" {
		t.Errorf("day window starts %q, want today — a reset day means nothing to a daily cap", got)
	}
	for _, p := range []string{model.TrafficMonth, "", "nonsense"} {
		if got := trafficPeriodStart(model.Placement{TrafficPeriod: p}, now); got != "2026-09-01" {
			t.Errorf("period %q starts %q, want the 1st", p, got)
		}
	}
}

// The billing month: the window opens on the reset day, reaches back into the previous
// month until that day comes round, and a day the month does not have is its last.
func TestTrafficPeriodStartOnAResetDay(t *testing.T) {
	t.Parallel()
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 13, 45, 0, 0, time.UTC) }
	for _, c := range []struct {
		name  string
		now   time.Time
		reset int
		want  string
	}{
		{"on the reset day", day(2026, 9, 14), 14, "2026-09-14"},
		{"after it", day(2026, 9, 17), 14, "2026-09-14"},
		{"the day before", day(2026, 9, 13), 14, "2026-08-14"},
		{"before it, across the new year", day(2026, 1, 10), 15, "2025-12-15"},
		{"the 31st in a 30-day month is its last day", day(2026, 9, 30), 31, "2026-09-30"},
		{"before that, last month had a 31st", day(2026, 9, 29), 31, "2026-08-31"},
		{"the 30th in February is the 28th", day(2026, 2, 28), 30, "2026-02-28"},
		{"just before it, January had a 30th", day(2026, 2, 27), 30, "2026-01-30"},
		{"March 1st with a reset on the 31st", day(2026, 3, 1), 31, "2026-02-28"},
		{"a leap February", day(2028, 2, 29), 31, "2028-02-29"},
		{"0 is the 1st", day(2026, 9, 17), 0, "2026-09-01"},
	} {
		p := model.Placement{TrafficPeriod: model.TrafficMonth, TrafficResetDay: c.reset}
		if got := trafficPeriodStart(p, c.now); got != c.want {
			t.Errorf("%s: %s with reset day %d starts %s, want %s", c.name, c.now.Format("2006-01-02"), c.reset, got, c.want)
		}
	}
	// A local midnight in the operator's zone, not UTC's: late evening in Moscow on the
	// 13th is the 13th there, before a reset on the 14th.
	msk := time.FixedZone("MSK", 3*3600)
	p := model.Placement{TrafficPeriod: model.TrafficMonth, TrafficResetDay: 14}
	if got := p.TrafficPeriodStart(time.Date(2026, 9, 13, 23, 30, 0, 0, msk)); !got.Equal(time.Date(2026, 8, 14, 0, 0, 0, 0, msk)) {
		t.Errorf("Moscow, 13th 23:30: period starts %v, want 14 Aug 00:00 MSK", got)
	}
}

func TestTrafficResetDayValidateAndNormalize(t *testing.T) {
	t.Parallel()
	for _, d := range []int{-1, 32} {
		if err := (model.Placement{TrafficLimit: 1, TrafficResetDay: d}).Validate(); err == nil {
			t.Errorf("reset day %d was accepted", d)
		}
	}
	for _, d := range []int{0, 1, 28, 31} {
		if err := (model.Placement{TrafficLimit: 1, TrafficResetDay: d}).Validate(); err != nil {
			t.Errorf("reset day %d was refused: %v", d, err)
		}
	}
	for _, c := range []struct {
		name string
		in   model.Placement
		want int
	}{
		{"a monthly cap keeps its day", model.Placement{TrafficLimit: 1, TrafficResetDay: 14}, 14},
		{"the 1st is the default, stored as 0", model.Placement{TrafficLimit: 1, TrafficResetDay: 1}, 0},
		{"a daily cap has no reset day", model.Placement{TrafficLimit: 1, TrafficPeriod: model.TrafficDay, TrafficResetDay: 14}, 0},
		{"no cap, no reset day", model.Placement{TrafficResetDay: 14}, 0},
	} {
		if got := c.in.Normalized().TrafficResetDay; got != c.want {
			t.Errorf("%s: normalized to %d, want %d", c.name, got, c.want)
		}
	}
}

// Clearing the limit has to clear what hangs off it. A stored "hide when over" with
// no limit to be over would be a switch that does nothing and reads as if it does.
func TestPlacementNormalizeClearsTheCap(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// The reset day is stored for the master and for a node, and it is what the usage is
// summed from. The clock is real here, so the dates are picked around today: traffic
// yesterday and today, and a reset day of today puts only today's in the window.
func TestRefreshNodeTrafficCountsFromTheResetDay(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "reset.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st}

	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	n, err := st.CreateNode("billed", "billed.example.com", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	now := time.Now().In(m.loc())
	today, yesterday := now.Format("2006-01-02"), now.AddDate(0, 0, -1).Format("2006-01-02")
	for _, id := range []int64{model.LocalNodeID, n.ID} {
		if err := st.AddDailyTrafficNode(u.ID, id, today, 1<<30, 0); err != nil {
			t.Fatal(err)
		}
		if err := st.AddDailyTrafficNode(u.ID, id, yesterday, 5<<30, 0); err != nil {
			t.Fatal(err)
		}
	}

	resetToday := model.Placement{TrafficLimit: 100 << 30, TrafficResetDay: now.Day()}
	if err := st.SetMasterPlacement(resetToday); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNode(n.ID, store.NodeEdit{Name: "billed", Host: "billed.example.com", Placement: resetToday}); err != nil {
		t.Fatal(err)
	}
	want := resetToday.Normalized().TrafficResetDay // 0 on the 1st, which is the same day
	if set, _ := st.GetSettings(); set.MasterPlacement.TrafficResetDay != want {
		t.Errorf("master reset day read back as %d, want %d", set.MasterPlacement.TrafficResetDay, want)
	}
	if got, _ := st.GetNode(n.ID); got == nil || got.TrafficResetDay != want {
		t.Errorf("node reset day read back as %+v, want %d", got, want)
	}

	m.refreshNodeTraffic()
	for _, id := range []int64{model.LocalNodeID, n.ID} {
		if got := m.NodeTrafficUsage(id).Used; got != 1<<30 {
			t.Errorf("server %d, reset day today: used %d, want only today's 1 GiB", id, got)
		}
	}

	// From the 1st, yesterday is in the window — unless today is the 1st.
	if now.Day() != 1 {
		if err := st.SetMasterPlacement(model.Placement{TrafficLimit: 100 << 30}); err != nil {
			t.Fatal(err)
		}
		m.refreshNodeTraffic()
		if got := m.NodeTrafficUsage(model.LocalNodeID).Used; got != 6<<30 {
			t.Errorf("reset on the 1st: used %d, want yesterday's and today's 6 GiB", got)
		}
	}
}
