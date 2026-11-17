package core

import (
	"context"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

func setRenewLimits(t *testing.T, st *store.Store, window, maxDays int, periods []model.PeriodOffer) {
	t.Helper()
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	set.RenewWindowDays, set.MaxPrepaidDays, set.BillingPeriods = window, maxDays, periods
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatalf("billing settings: %v", err)
	}
}

func expireIn(t *testing.T, st *store.Store, id int64, secs int64) {
	t.Helper()
	if err := st.SetUserLimits(id, 0, time.Now().Unix()+secs, 0); err != nil {
		t.Fatalf("limits: %v", err)
	}
}

// The window: a renewal is refused until the last N days of the term, then sold.
func TestRenewWindowHoldsRenewalBack(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	setRenewLimits(t, st, 10, 0, nil)
	u := walletUser(t, st, "window")
	credit(t, m, u.ID, 100000)
	ctx := context.Background()

	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("first purchase: %v", err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); !isCode(err, "err.renewNotYet") {
		t.Fatalf("renewal 30 days before the end: want err.renewNotYet, got %v", err)
	}
	set, _ := m.Settings()
	got, _ := st.GetUser(u.ID)
	if at := m.RenewOpensAt(set, *got, plan, time.Now().Unix()); at != got.ExpireAt-10*86400 {
		t.Fatalf("opens at %d, want %d", at, got.ExpireAt-10*86400)
	}
	expireIn(t, st, u.ID, 5*86400)
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("renewal 5 days before the end: %v", err)
	}
}

// The cap: several periods at once beyond it are neither offered nor sold, and with
// the cap filled a renewal waits until one period fits again.
func TestPrepaidCapLimitsTermsAndRenewals(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	setRenewLimits(t, st, 0, 60, []model.PeriodOffer{{Periods: 2, Percent: 5}, {Periods: 3, Percent: 10}})
	u := walletUser(t, st, "cap")
	credit(t, m, u.ID, 500000)
	ctx := context.Background()

	offers := m.PeriodOffers(*u, plan)
	if len(offers) != 2 || offers[1].Periods != 2 {
		t.Fatalf("offers: want 1 and 2 periods, got %+v", offers)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 3); !isCode(err, "err.prepaidTooLong") {
		t.Fatalf("90 days with a 60-day cap: want err.prepaidTooLong, got %v", err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("one period: %v", err)
	}
	// 30 days left + 30 = 60: fits once, and only one period.
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 2); !isCode(err, "err.prepaidTooLong") {
		t.Fatalf("30 left + 60: want err.prepaidTooLong, got %v", err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("renewal up to the cap: %v", err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); !isCode(err, "err.renewNotYet") {
		t.Fatalf("cap full: want err.renewNotYet, got %v", err)
	}
	got, _ := st.GetUser(u.ID)
	set, _ := m.Settings()
	if at := m.RenewOpensAt(set, *got, plan, time.Now().Unix()); at != got.ExpireAt-30*86400 {
		t.Fatalf("opens at %d, want 30 days before the end %d", at, got.ExpireAt-30*86400)
	}
}

// A plan's own limit beats the settings', and 0 on the plan takes them.
func TestPlanRenewLimitOverridesSettings(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	setRenewLimits(t, st, 20, 0, nil)
	u := walletUser(t, st, "override")
	credit(t, m, u.ID, 100000)
	ctx := context.Background()
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("first purchase: %v", err)
	}
	expireIn(t, st, u.ID, 15*86400)
	plan.RenewWindowDays = 3
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); !isCode(err, "err.renewNotYet") {
		t.Fatalf("plan window 3 days, 15 left: want err.renewNotYet, got %v", err)
	}
	plan.RenewWindowDays = 0
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("settings window 20 days, 15 left: %v", err)
	}
}

// Two payment links opened inside the window and both paid: the first renews, the
// second finds the window closed and its money goes to the balance.
func TestRenewWindowAtConfirmCreditsBalance(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	setRenewLimits(t, st, 10, 0, nil)
	u := walletUser(t, st, "confirm")
	ctx := context.Background()
	if err := m.ApplyPlanToUser(ctx, u.ID, plan.ID, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	expireIn(t, st, u.ID, 5*86400)
	a, err := m.RequestPurchaseExternal(ctx, u.ID, PlanPurchase(plan.ID, 1), "ref-a")
	if err != nil {
		t.Fatalf("order a: %v", err)
	}
	b, err := m.RequestPurchaseExternal(ctx, u.ID, PlanPurchase(plan.ID, 1), "ref-b")
	if err != nil {
		t.Fatalf("order b: %v", err)
	}
	if err := m.ConfirmPayment(ctx, a.ID); err != nil {
		t.Fatalf("confirm a: %v", err)
	}
	after, _ := st.GetUser(u.ID)
	if err := m.ConfirmPayment(ctx, b.ID); err != nil {
		t.Fatalf("confirm b: %v", err)
	}
	final, _ := st.GetUser(u.ID)
	if final.ExpireAt != after.ExpireAt {
		t.Fatalf("second order renewed again: %d → %d", after.ExpireAt, final.ExpireAt)
	}
	if bal := balanceOf(t, st, u.ID); bal != int64(plan.PriceRub)*100 {
		t.Fatalf("balance %d, want the second payment %d", bal, plan.PriceRub*100)
	}
}

// When a renewal opens, by the days left, for a 30-day plan: the window alone, the
// cap alone (left ≤ cap − period, never below one period), and both (the stricter).
func TestRenewOpensAtTable(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "table")
	if err := m.ApplyPlanToUser(context.Background(), u.ID, plan.ID, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	u, _ = st.GetUser(u.ID)
	const day = 86400
	cases := []struct {
		window, maxDays int
		left            int64 // days
		open            bool
	}{
		{0, 0, 30, true},
		{10, 0, 11, false}, {10, 0, 10, true},
		{0, 30, 5, false}, {0, 30, 0, true}, // cap = period: only once the term is over (0 = ended)
		{0, 10, 1, false}, // a cap below a period counts as one period
		{0, 45, 16, false}, {0, 45, 15, true},
		{0, 60, 30, true}, {0, 60, 31, false},
		{0, 30, 1, false}, // cap = period: a day before the end is still too early
		{0, 90, 60, true},
		{20, 45, 16, false}, {20, 45, 15, true}, // cap is the stricter one
		{5, 90, 6, false}, {5, 90, 5, true}, // window is the stricter one
	}
	for _, c := range cases {
		set := &model.Settings{RenewWindowDays: c.window, MaxPrepaidDays: c.maxDays}
		now := time.Now().Unix()
		got := *u
		got.ExpireAt = now + c.left*day
		if c.left == 0 {
			got.ExpireAt = now - 60 // the term ended a minute ago
		}
		at := m.RenewOpensAt(set, got, plan, now)
		if (at == 0) != c.open {
			t.Errorf("window %d, cap %d, %d days left: open=%v, want %v (opens %d)",
				c.window, c.maxDays, c.left, at == 0, c.open, at)
		}
	}
}
