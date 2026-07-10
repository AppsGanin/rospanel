package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// TestCancelAndSwitchGuard covers the "renew or cancel, no direct switch" rule:
// while a paid plan is active, buying a different plan is blocked; cancelling moves
// the user to the free plan, after which any paid plan can be bought.
func TestCancelAndSwitchGuard(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	free := &model.TariffPlan{Slug: "tc-free", Name: "Бесплатный-т", PriceRub: 0, Enabled: true}
	std := &model.TariffPlan{Slug: "tc-std", Name: "Стандарт-т", PriceRub: 199, PeriodDays: 30, Enabled: true}
	pro := &model.TariffPlan{Slug: "tc-pro", Name: "Про-т", PriceRub: 499, PeriodDays: 30, Enabled: true}
	for _, p := range []*model.TariffPlan{free, std, pro} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save plan %s: %v", p.Slug, err)
		}
	}
	set, _ := st.GetSettings()
	set.BillingEnabled = true
	set.BillingFreePlanID = free.ID
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatalf("billing settings: %v", err)
	}

	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Put the user on an active paid plan.
	if err := m.ApplyPlanToUser(u.ID, std.ID, true); err != nil {
		t.Fatalf("apply std: %v", err)
	}
	cur, _ := st.GetUser(u.ID)
	if active := m.ActivePaidPlan(*cur); active == nil || active.ID != std.ID {
		t.Fatalf("ActivePaidPlan should be Стандарт, got %+v", active)
	}

	// Switching to a DIFFERENT paid plan is blocked while one is active.
	if _, err := m.startPlanPayment(u.ID, pro.ID, "cryptobot", ""); err == nil ||
		!strings.Contains(err.Error(), "отмените") {
		t.Fatalf("switch to Про should be blocked with a cancel hint, got err=%v", err)
	}
	// Renewing the SAME plan passes the guard (fails later only on provider config).
	if _, err := m.startPlanPayment(u.ID, std.ID, "cryptobot", ""); err != nil &&
		strings.Contains(err.Error(), "отмените") {
		t.Fatalf("renewing the same plan must not be blocked by the switch guard: %v", err)
	}

	// Cancel → user lands on the free plan, expiry cleared.
	if err := m.CancelUserPlan(u.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	after, _ := st.GetUser(u.ID)
	if after.PlanID != free.ID {
		t.Fatalf("after cancel plan = %d, want free %d", after.PlanID, free.ID)
	}
	if after.ExpireAt != 0 {
		t.Fatalf("after cancel expiry = %d, want 0 (free)", after.ExpireAt)
	}
	if m.ActivePaidPlan(*after) != nil {
		t.Fatal("free plan must not count as an active paid plan")
	}

	// Now on free, buying any paid plan passes the guard.
	if _, err := m.startPlanPayment(u.ID, pro.ID, "cryptobot", ""); err != nil &&
		strings.Contains(err.Error(), "отмените") {
		t.Fatalf("buying a paid plan after cancel must be allowed: %v", err)
	}
}

// TestRequestPlanPaymentReusesPendingOrder verifies the anti-spam behaviour: a
// second manual-order request for the same user+plan reuses the pending order
// instead of creating a duplicate.
func TestRequestPlanPaymentReusesPendingOrder(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "reuse.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	plan := &model.TariffPlan{Slug: "rp-std", Name: "Стандарт-rp", PriceRub: 199, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	o1, _, err := m.RequestPlanPayment(u.ID, plan.ID)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	o2, _, err := m.RequestPlanPayment(u.ID, plan.ID)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if o1.ID != o2.ID {
		t.Fatalf("expected the pending order to be reused: o1=%d o2=%d", o1.ID, o2.ID)
	}
	pending, err := st.ListPaymentOrders("pending", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected exactly 1 pending order, got %d", len(pending))
	}
}

// TestExtendOnlyOnRenewal covers finding #3: buying a paid plan while a trial
// (future expiry, price 0) is active must start from NOW, not inherit the trial's
// remaining time; renewing the same active paid plan must extend from its expiry.
func TestExtendOnlyOnRenewal(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "renew.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	trial := &model.TariffPlan{Slug: "er-trial", Name: "Проб-er", PriceRub: 0, PeriodDays: 3, Enabled: true}
	std := &model.TariffPlan{Slug: "er-std", Name: "Стд-er", PriceRub: 199, PeriodDays: 30, Enabled: true}
	for _, p := range []*model.TariffPlan{trial, std} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save %s: %v", p.Slug, err)
		}
	}
	u, _ := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)

	// Put the user on an active trial (future expiry, price 0).
	now := time.Now().Unix()
	if err := st.SetUserLimits(u.ID, 0, now+3*86400, 0); err != nil {
		t.Fatalf("limits: %v", err)
	}
	if err := st.SetUserPlan(u.ID, trial.ID, true); err != nil {
		t.Fatalf("plan: %v", err)
	}

	// isPlanRenewal must be false (trial isn't an active PAID plan of std).
	if m.isPlanRenewal(u.ID, std.ID) {
		t.Fatal("buying std from trial must not count as a renewal")
	}
	// Simulate the confirm's decision: buy std → start from now, NOT trial expiry.
	if err := m.ApplyPlanToUser(u.ID, std.ID, m.isPlanRenewal(u.ID, std.ID)); err != nil {
		t.Fatalf("apply std: %v", err)
	}
	got, _ := st.GetUser(u.ID)
	want := now + 30*86400
	if d := got.ExpireAt - want; d < -5 || d > 5 {
		t.Fatalf("expiry after trial→paid = %d, want ~%d (now+30d, no trial carryover)", got.ExpireAt, want)
	}

	// Now renewing std (active paid) must extend from its current expiry.
	if !m.isPlanRenewal(u.ID, std.ID) {
		t.Fatal("renewing the active std plan must count as a renewal")
	}
	prev := got.ExpireAt
	if err := m.ApplyPlanToUser(u.ID, std.ID, m.isPlanRenewal(u.ID, std.ID)); err != nil {
		t.Fatalf("renew std: %v", err)
	}
	got2, _ := st.GetUser(u.ID)
	if d := got2.ExpireAt - (prev + 30*86400); d < -5 || d > 5 {
		t.Fatalf("renewal expiry = %d, want ~%d (prev+30d)", got2.ExpireAt, prev+30*86400)
	}
}

// TestRequestPlanPaymentSwitchGuard covers finding #1: the manual-order path also
// blocks switching to a different plan while a paid one is active.
func TestRequestPlanPaymentSwitchGuard(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "manguard.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	a := &model.TariffPlan{Slug: "mg-a", Name: "A-mg", PriceRub: 199, PeriodDays: 30, Enabled: true}
	b := &model.TariffPlan{Slug: "mg-b", Name: "B-mg", PriceRub: 499, PeriodDays: 30, Enabled: true}
	for _, p := range []*model.TariffPlan{a, b} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	u, _ := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err := m.ApplyPlanToUser(u.ID, a.ID, true); err != nil {
		t.Fatalf("apply a: %v", err)
	}
	// Manual order for a DIFFERENT plan while A is active → blocked.
	if _, _, err := m.RequestPlanPayment(u.ID, b.ID); err == nil || !contains(err.Error(), "отмените") {
		t.Fatalf("manual order for B must be blocked while A active, got %v", err)
	}
	// Manual order for the SAME plan (renewal) → allowed.
	if _, _, err := m.RequestPlanPayment(u.ID, a.ID); err != nil {
		t.Fatalf("manual renewal of A must be allowed: %v", err)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// TestDisabledPlanNotPurchasable covers the security fix: a disabled plan can't be
// bought by passing its id to the pay paths, even though GetTariffPlan returns it.
func TestDisabledPlanNotPurchasable(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "disabled.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	p := &model.TariffPlan{Slug: "dp-x", Name: "Retired-dp", PriceRub: 199, PeriodDays: 30, Enabled: false}
	if err := st.SaveTariffPlan(p); err != nil {
		t.Fatalf("save: %v", err)
	}
	u, _ := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)

	if _, err := m.startPlanPayment(u.ID, p.ID, "cryptobot", ""); err == nil || !contains(err.Error(), "недоступен") {
		t.Fatalf("auto pay for disabled plan should be rejected, got %v", err)
	}
	if _, _, err := m.RequestPlanPayment(u.ID, p.ID); err == nil || !contains(err.Error(), "недоступен") {
		t.Fatalf("manual order for disabled plan should be rejected, got %v", err)
	}

	// But an existing subscriber ON the disabled plan may still RENEW it.
	if err := st.SetUserPlan(u.ID, p.ID, false); err != nil {
		t.Fatalf("put user on disabled plan: %v", err)
	}
	if _, _, err := m.RequestPlanPayment(u.ID, p.ID); err != nil {
		t.Fatalf("renewing your own (now-disabled) plan must be allowed: %v", err)
	}
	// Auto path renewal passes the enabled/switch guards (only later needs a provider).
	if _, err := m.startPlanPayment(u.ID, p.ID, "cryptobot", ""); err != nil &&
		(contains(err.Error(), "недоступен") || contains(err.Error(), "отмените")) {
		t.Fatalf("auto renewal of your own disabled plan must not be blocked by guards: %v", err)
	}
}

// TestLatestPendingProviderOrderForPlan checks the anti-spam reuse query returns
// the pending provider order for a user+plan+provider.
func TestLatestPendingProviderOrderForPlan(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "reuseq.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	p := &model.TariffPlan{Slug: "lp-x", Name: "P-lp", PriceRub: 199, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(p); err != nil {
		t.Fatalf("save: %v", err)
	}
	u, _ := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)

	// No provider order yet.
	if _, err := st.LatestPendingProviderOrderForPlan(u.ID, p.ID, "cryptobot"); err == nil {
		t.Fatal("expected no order before creation")
	}
	o, err := st.CreatePaymentOrder(u.ID, p.ID, p.PriceRub)
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	// Still manual (no provider) → not matched.
	if _, err := st.LatestPendingProviderOrderForPlan(u.ID, p.ID, "cryptobot"); err == nil {
		t.Fatal("manual order must not match provider query")
	}
	if err := st.SetPaymentOrderProvider(o.ID, "cryptobot", "inv-1", "https://pay/x"); err != nil {
		t.Fatalf("set provider: %v", err)
	}
	got, err := st.LatestPendingProviderOrderForPlan(u.ID, p.ID, "cryptobot")
	if err != nil || got == nil || got.ID != o.ID || got.PayURL != "https://pay/x" {
		t.Fatalf("expected the pending provider order, got %+v err=%v", got, err)
	}
}

// TestMigratePlanUsers moves users off a plan onto another one.
func TestMigratePlanUsers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "migrate.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	from := &model.TariffPlan{Slug: "mp-from", Name: "From-mp", PriceRub: 199, PeriodDays: 30, Enabled: true}
	to := &model.TariffPlan{Slug: "mp-to", Name: "To-mp", PriceRub: 299, PeriodDays: 30, Enabled: true}
	for _, p := range []*model.TariffPlan{from, to} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		u, _ := st.CreateUser("u", "uuid"+string(rune('a'+i)), "pw", "tok"+string(rune('a'+i)), 0, 0, 0)
		if err := m.ApplyPlanToUser(u.ID, from.ID, false); err != nil {
			t.Fatalf("apply from: %v", err)
		}
	}
	if n, _ := st.CountUsersOnPlan(from.ID); n != 3 {
		t.Fatalf("expected 3 on from, got %d", n)
	}

	// Same-plan target is rejected.
	if _, err := m.MigratePlanUsers(from.ID, from.ID); err == nil {
		t.Fatal("same-plan migration should be rejected")
	}
	moved, err := m.MigratePlanUsers(from.ID, to.ID)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if moved != 3 {
		t.Fatalf("migrated = %d, want 3", moved)
	}
	if n, _ := st.CountUsersOnPlan(from.ID); n != 0 {
		t.Fatalf("from should be empty, got %d", n)
	}
	if n, _ := st.CountUsersOnPlan(to.ID); n != 3 {
		t.Fatalf("to should have 3, got %d", n)
	}
}
