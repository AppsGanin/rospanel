package core

import (
	"errors"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/importer"
	"github.com/AppsGanin/rospanel/internal/model"
)

// A term that starts on the first connection. The promise to the operator who sells
// one is exact: the days do not run while the key sits unused, they start the moment
// it is used, and nothing else ever moves a date that has been set.

const day = int64(86400)

func heldUser(t *testing.T, m *Manager, name string, days int64) *model.User {
	t.Helper()
	u, err := m.CreateUserWithTerm(adminCtx(), name, 0, 0, days*day)
	if err != nil {
		t.Fatalf("create %s on hold: %v", name, err)
	}
	return u
}

func connect(m *Manager, u *model.User, ip string) {
	m.RecordAccess(model.UserEmail(u.ID), ip, "")
	m.FlushAccess()
}

func TestHeldTermStartsOnTheFirstConnection(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	u := heldUser(t, m, "reseller-key", 30)
	if u.ExpireAt != 0 || u.HoldSeconds != 30*day {
		t.Fatalf("created: expire %d hold %d — want no date and 30 days pending", u.ExpireAt, u.HoldSeconds)
	}
	// Waiting is not being cut off: the key has to work to be used for the first time.
	working, err := m.store.WorkingUsers(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if !containsUser(working, u.ID) {
		t.Fatal("a user on hold is not in the working set — they could never make the first connection")
	}

	before := time.Now().Unix()
	connect(m, u, "198.51.100.4")
	after := time.Now().Unix()
	got, _ := m.store.GetUser(u.ID)
	if got.HoldSeconds != 0 {
		t.Errorf("after the first connection the hold is still %d", got.HoldSeconds)
	}
	if got.ExpireAt < before+30*day || got.ExpireAt > after+30*day {
		t.Errorf("term started to %d, want 30 days from the connection (%d..%d)", got.ExpireAt, before+30*day, after+30*day)
	}
	starts := 0
	for _, e := range trail(t, m, u.ID) {
		if e.Action == model.EventTermStarted {
			starts++
			d, _ := e.Details.(map[string]any)
			if end, _ := d["expire_at"].(float64); int64(end) != got.ExpireAt {
				t.Errorf("the journal row says the term ends %v, want %d", d["expire_at"], got.ExpireAt)
			}
		}
	}
	if starts != 1 {
		t.Errorf("term started %d times in the journal, want once", starts)
	}

	// Later connections are ordinary ones.
	m.accLast = map[string]int64{} // past the per-address throttle
	connect(m, u, "198.51.100.5")
	again, _ := m.store.GetUser(u.ID)
	if again.ExpireAt != got.ExpireAt {
		t.Errorf("a second connection moved the date from %d to %d", got.ExpireAt, again.ExpireAt)
	}
	for _, e := range trail(t, m, u.ID) {
		if e.Action == model.EventTermStarted {
			starts--
		}
	}
	if starts != 0 {
		t.Error("a second connection started the term again")
	}
}

// Only the users who connected start; everyone else on hold keeps waiting.
func TestOnlyTheUsersWhoConnectStart(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	a := heldUser(t, m, "a", 30)
	b := heldUser(t, m, "b", 7)
	connect(m, a, "198.51.100.10")
	if got, _ := m.store.GetUser(b.ID); got.ExpireAt != 0 || got.HoldSeconds != 7*day {
		t.Errorf("b never connected but has expire %d hold %d", got.ExpireAt, got.HoldSeconds)
	}
}

// A date set by hand replaces the pending term; a limits save without a date — the
// form posting quota and devices for a user on hold — leaves it alone.
func TestLimitsKeepAHoldUntilADateIsSet(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	u := heldUser(t, m, "limits", 30)

	if err := m.SetUserLimits(ctx, u.ID, 5<<30, 0, 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.GetUser(u.ID); got.HoldSeconds != 30*day || got.DataLimit != 5<<30 {
		t.Fatalf("a quota save without a date: hold %d quota %d — want the hold kept", got.HoldSeconds, got.DataLimit)
	}

	date := time.Now().Add(90 * 24 * time.Hour).Unix()
	if err := m.SetUserLimits(ctx, u.ID, 5<<30, date, 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.GetUser(u.ID); got.HoldSeconds != 0 || got.ExpireAt != date {
		t.Fatalf("a date: hold %d expire %d — want the date and no hold", got.HoldSeconds, got.ExpireAt)
	}
	connect(m, u, "198.51.100.20")
	if got, _ := m.store.GetUser(u.ID); got.ExpireAt != date {
		t.Errorf("a connection moved the date set by hand from %d to %d", date, got.ExpireAt)
	}
}

// A plan owns the term. Assigning one takes a hold away, and the next connection
// must not start it on top of the plan's own date.
func TestAPlanReplacesAHold(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	plan := &model.TariffPlan{Slug: "hold-std", Name: "Std", PriceRub: 300, PeriodDays: 30, Enabled: true}
	if err := m.store.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	u := heldUser(t, m, "buyer", 365)
	if err := m.ApplyPlanToUser(adminCtx(), u.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	planned, _ := m.store.GetUser(u.ID)
	if planned.HoldSeconds != 0 || planned.ExpireAt == 0 {
		t.Fatalf("after the plan: expire %d hold %d — want the plan's date and no hold", planned.ExpireAt, planned.HoldSeconds)
	}
	connect(m, u, "198.51.100.30")
	if got, _ := m.store.GetUser(u.ID); got.ExpireAt != planned.ExpireAt {
		t.Errorf("a connection replaced the plan's date %d with %d", planned.ExpireAt, got.ExpireAt)
	}

	// A free plan has no date at all — and still no hold to start later.
	free := &model.TariffPlan{Slug: "hold-free", Name: "Free", Enabled: true}
	if err := m.store.SaveTariffPlan(free); err != nil {
		t.Fatal(err)
	}
	v := heldUser(t, m, "freeloader", 30)
	if err := m.ApplyPlanToUser(adminCtx(), v.ID, free.ID, false); err != nil {
		t.Fatal(err)
	}
	connect(m, v, "198.51.100.31")
	if got, _ := m.store.GetUser(v.ID); got.HoldSeconds != 0 || got.ExpireAt != 0 {
		t.Errorf("free plan: expire %d hold %d — the old hold started under a plan with no end", got.ExpireAt, got.HoldSeconds)
	}
}

func TestSetUserHold(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	past := time.Now().Add(-24 * time.Hour).Unix()
	u, err := m.CreateUser(ctx, "lapsed", 0, past)
	if err != nil {
		t.Fatal(err)
	}
	// An expired key handed out again, to start whenever it is picked up.
	if err := m.SetUserHold(ctx, u.ID, 14*day); err != nil {
		t.Fatal(err)
	}
	got, _ := m.store.GetUser(u.ID)
	if got.ExpireAt != 0 || got.HoldSeconds != 14*day || got.Status != model.StatusActive {
		t.Fatalf("on hold: expire %d hold %d status %s — want no date, 14 days, active", got.ExpireAt, got.HoldSeconds, got.Status)
	}
	if err := m.SetUserHold(ctx, u.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.GetUser(u.ID); got.HoldSeconds != 0 || got.ExpireAt != 0 {
		t.Errorf("hold taken away: expire %d hold %d — want neither", got.ExpireAt, got.HoldSeconds)
	}

	for _, bad := range []int64{-1, int64(maxExtendDays)*day + 1} {
		if err := m.SetUserHold(ctx, u.ID, bad); err == nil {
			t.Errorf("a hold of %d seconds was accepted", bad)
		}
	}
	if _, err := m.CreateUserWithTerm(ctx, "both", 0, time.Now().Add(time.Hour).Unix(), 30*day); err == nil {
		t.Error("a user with both a date and a pending term was created")
	}
}

// Extending a user whose term has not started lengthens the term they will get,
// instead of silently skipping them.
func TestBulkExtendLengthensAHeldTerm(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	held := heldUser(t, m, "held", 30)
	dated, _ := m.CreateUser(ctx, "dated", 0, time.Now().Add(10*24*time.Hour).Unix())
	never, _ := m.CreateUser(ctx, "never", 0, 0)

	n, err := m.BulkUserAction(ctx, []int64{held.ID, dated.ID, never.ID}, "extend", 10)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("extended %d users, want 2 (the held and the dated one)", n)
	}
	if got, _ := m.store.GetUser(held.ID); got.HoldSeconds != 40*day || got.ExpireAt != 0 {
		t.Errorf("held: hold %d expire %d — want 40 days pending, still no date", got.HoldSeconds, got.ExpireAt)
	}
	if got, _ := m.store.GetUser(never.ID); got.ExpireAt != 0 || got.HoldSeconds != 0 {
		t.Errorf("never-expiring user got expire %d hold %d", got.ExpireAt, got.HoldSeconds)
	}
}

// A held term comes in from another panel and goes out in this panel's export.
func TestImportAndExportCarryAHeldTerm(t *testing.T) {
	t.Parallel()
	src := bulkTestManager(t)
	u := heldUser(t, src, "exported", 30)
	exp, err := src.ExportUsers()
	if err != nil {
		t.Fatal(err)
	}
	dst := bulkTestManager(t)
	res, err := dst.ImportUsers(adminCtx(), ImportRequest{Source: "rospanel", Users: exp.Users})
	if err != nil || res.Created != 1 {
		t.Fatalf("import: %+v %v", res, err)
	}
	list, _ := dst.store.ListUsers()
	if len(list) != 1 || list[0].HoldSeconds != 30*day || list[0].ExpireAt != 0 || list[0].UUID != u.UUID {
		t.Fatalf("round trip: %+v", list)
	}

	// A candidate with a date and a hold is a started term; an absurd hold is capped.
	third := bulkTestManager(t)
	date := time.Now().Add(48 * time.Hour).Unix()
	res, err = third.ImportUsers(adminCtx(), ImportRequest{Source: "marzban", Users: []importer.Candidate{
		{Name: "started", UUID: "5d4c1b5e-7f0a-4c55-9a52-7b1f0c0b7a11", Password: "pw", ExpireAt: date, HoldSeconds: 30 * day, Enabled: true},
		{Name: "century", UUID: "0f7d8a3c-2b8e-4d6f-8e2a-3c9b4a1d5e22", Password: "pw", HoldSeconds: 100 * 365 * day, Enabled: true},
	}})
	if err != nil || res.Created != 2 {
		t.Fatalf("import: %+v %v", res, err)
	}
	for _, x := range mustUsers(t, third) {
		switch x.Name {
		case "started":
			if x.ExpireAt != date || x.HoldSeconds != 0 {
				t.Errorf("started: expire %d hold %d — want the date only", x.ExpireAt, x.HoldSeconds)
			}
		case "century":
			if x.HoldSeconds != int64(maxExtendDays)*day {
				t.Errorf("century: hold %d, want capped at %d", x.HoldSeconds, int64(maxExtendDays)*day)
			}
		}
	}
}

func TestNameVariablesForAHeldTerm(t *testing.T) {
	t.Parallel()
	u := &model.User{HoldSeconds: 30 * day}
	got := model.RenderName("{days}d {expire}", model.NameVars{User: u})
	if got != "30d "+model.NameUnknown {
		t.Errorf("held user renders %q — want the whole term in days and no date", got)
	}
}

func mustUsers(t *testing.T, m *Manager) []model.User {
	t.Helper()
	list, err := m.store.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func containsUser(list []model.User, id int64) bool {
	for _, u := range list {
		if u.ID == id {
			return true
		}
	}
	return false
}

// The stale card. The form was opened on a user still waiting for their first
// connection; they connected; then the operator saved. A quota-only save must leave
// the started term alone, and a save that changes the term must be refused rather
// than hand the user a fresh hold on top of the days they have already begun.
func TestAStaleCardCannotRestartATerm(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	u := heldUser(t, m, "picked-up", 30)
	seenExpire, seenHold := u.ExpireAt, u.HoldSeconds

	connect(m, u, "198.51.100.40")
	started, _ := m.store.GetUser(u.ID)
	if started.ExpireAt == 0 {
		t.Fatal("the connection did not start the term")
	}

	if err := m.SetUserQuota(ctx, u.ID, 7<<30, 3); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.GetUser(u.ID); got.ExpireAt != started.ExpireAt || got.HoldSeconds != 0 || got.DataLimit != 7<<30 || got.DeviceLimit != 3 {
		t.Fatalf("quota save: expire %d hold %d quota %d devices %d — want the started term untouched", got.ExpireAt, got.HoldSeconds, got.DataLimit, got.DeviceLimit)
	}

	err := m.SetUserLimitsSeen(ctx, u.ID, 8<<30, 0, 45*day, 3, seenExpire, seenHold)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Code != "err.userTermChanged" {
		t.Fatalf("stale term save: %v — want err.userTermChanged", err)
	}
	if got, _ := m.store.GetUser(u.ID); got.ExpireAt != started.ExpireAt || got.HoldSeconds != 0 || got.DataLimit != 7<<30 {
		t.Errorf("the refused save wrote: expire %d hold %d quota %d", got.ExpireAt, got.HoldSeconds, got.DataLimit)
	}

	// From an up-to-date picture the same change goes through.
	if err := m.SetUserLimitsSeen(ctx, u.ID, 8<<30, 0, 45*day, 3, started.ExpireAt, 0); err != nil {
		t.Fatalf("fresh term save: %v", err)
	}
	if got, _ := m.store.GetUser(u.ID); got.ExpireAt != 0 || got.HoldSeconds != 45*day || got.DataLimit != 8<<30 {
		t.Errorf("fresh save: expire %d hold %d quota %d", got.ExpireAt, got.HoldSeconds, got.DataLimit)
	}
	if err := m.SetUserLimitsSeen(ctx, u.ID, 8<<30, time.Now().Add(time.Hour).Unix(), 45*day, 3, 0, 45*day); err == nil {
		t.Error("a save with both a date and a hold was accepted")
	}
}

// The check and the write are one statement: a term that moves after the manager
// read it still refuses the write.
func TestTermGuardedWriteRefusesAMovedTerm(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	u := heldUser(t, m, "raced", 30)
	connect(m, u, "198.51.100.41")
	ok, err := m.store.SetUserLimitsIfTerm(u.ID, 1, 0, 60*day, 0, 0, 30*day)
	if err != nil || ok {
		t.Fatalf("guarded write against a moved term: ok=%v err=%v — want refused", ok, err)
	}
	if got, _ := m.store.GetUser(u.ID); got.HoldSeconds != 0 || got.ExpireAt == 0 || got.DataLimit != 0 {
		t.Errorf("the refused write landed: %+v", got)
	}
}
