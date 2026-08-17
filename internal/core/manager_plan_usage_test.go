package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// planUsageFixture is a manager with a free plan, a paid plan, and one user who has
// already spent 20 GB — the shape every case below starts from.
func planUsageFixture(t *testing.T) (*Manager, *store.Store, model.User, *model.TariffPlan, *model.TariffPlan) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "planusage.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := &Manager{store: st}

	// The free plan is the one a fresh install ships with (migration 0007 seeds it and
	// designates it), so this is the real downgrade target rather than a lookalike.
	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	free, err := st.GetTariffPlan(set.BillingFreePlanID)
	if err != nil {
		t.Fatalf("free plan: %v", err)
	}
	free.PeriodDays = 30 // the seeded row predates rolling refills (migration 0017)
	if err := st.SaveTariffPlan(free); err != nil {
		t.Fatalf("save free: %v", err)
	}
	paid := &model.TariffPlan{Slug: "usage-std", Name: "Standard", PriceRub: 199, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(paid); err != nil {
		t.Fatalf("save paid: %v", err)
	}
	set.BillingEnabled = true
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	addUsage(t, st, u.ID, 5<<30, 15<<30)
	return m, st, *u, free, paid
}

// addUsage puts traffic on the counter the way a stats poll would.
func addUsage(t *testing.T, st *store.Store, id, up, down int64) {
	t.Helper()
	if err := st.UpdateTraffic(id, up, down, up, down); err != nil {
		t.Fatalf("usage: %v", err)
	}
}

func usedBy(t *testing.T, st *store.Store, id int64) int64 {
	t.Helper()
	u, err := st.GetUser(id)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	return u.UsedUp + u.UsedDown
}

// The bug this guards: a paid plan runs out, EnforceBilling hands over the free plan
// with a fresh 30-day cycle, and the 20 GB spent on the paid one stays on the counter.
// A 1 GB allowance is then over budget the instant it is granted, and the user is cut
// off until the cycle rolls a month later.
func TestPlanDowngradeResetsUsage(t *testing.T) {
	m, st, u, free, paid := planUsageFixture(t)
	ctx := context.Background()

	if err := m.ApplyPlanToUser(ctx, u.ID, paid.ID, false); err != nil {
		t.Fatalf("apply paid: %v", err)
	}
	// Spend the paid plan's traffic, then let it expire.
	addUsage(t, st, u.ID, 5<<30, 15<<30)
	if err := st.SetUserLimits(u.ID, 0, time.Now().Unix()-60, 0); err != nil {
		t.Fatalf("expire: %v", err)
	}

	if err := m.EnforceBilling(time.Now().Unix()); err != nil {
		t.Fatalf("enforce: %v", err)
	}

	after, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.PlanID != free.ID {
		t.Fatalf("plan = %d, want the free plan %d", after.PlanID, free.ID)
	}
	if used := after.UsedUp + after.UsedDown; used != 0 {
		t.Errorf("usage carried into the free plan: %d bytes — the new quota starts already spent", used)
	}
	if after.UsedUp+after.UsedDown >= after.DataLimit {
		t.Errorf("the user is over the free quota (%d/%d) right after the downgrade", after.UsedUp+after.UsedDown, after.DataLimit)
	}
}

// Buying a plan is a change of plan, so it starts a fresh quota — otherwise someone
// who exhausted the free allowance would pay and stay blocked.
func TestBuyingAPlanResetsUsage(t *testing.T) {
	m, st, u, free, paid := planUsageFixture(t)
	ctx := context.Background()

	if err := m.ApplyPlanToUser(ctx, u.ID, free.ID, false); err != nil {
		t.Fatalf("apply free: %v", err)
	}
	addUsage(t, st, u.ID, 1<<30, 1<<30)
	if err := m.ApplyPlanToUser(ctx, u.ID, paid.ID, false); err != nil {
		t.Fatalf("apply paid: %v", err)
	}
	if used := usedBy(t, st, u.ID); used != 0 {
		t.Errorf("usage after buying a different plan = %d, want 0", used)
	}
}

// Re-applying the plan the user is already on is not a new period: it tops up the
// time they had left, so the counter it was running stays. An operator re-assigning
// the same plan must not silently hand out a free refill either.
func TestSamePlanKeepsUsage(t *testing.T) {
	m, st, u, _, paid := planUsageFixture(t)
	ctx := context.Background()

	if err := m.ApplyPlanToUser(ctx, u.ID, paid.ID, false); err != nil {
		t.Fatalf("apply paid: %v", err)
	}
	addUsage(t, st, u.ID, 1<<30, 2<<30)
	before := usedBy(t, st, u.ID)
	if err := m.ApplyPlanToUser(ctx, u.ID, paid.ID, true); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if used := usedBy(t, st, u.ID); used != before {
		t.Errorf("renewing the same plan changed the counter: %d → %d", before, used)
	}
}

// The money bug this guards: a paid plan's quota has no rolling refill (planLimits gives
// paid plans ResetPeriod "none"), and re-assigning the same plan deliberately keeps the
// counter. Together that made the ONE path that never refills the one the user pays for —
// burn the quota mid-period, press "продлить", and the payment bought fresh time on a
// spent counter, so WorkingUsers kept filtering the user out with the money taken.
// A PURCHASED period refills; an operator re-assign still does not (TestSamePlanKeepsUsage).
func TestPaidRenewalRefillsTheQuota(t *testing.T) {
	m, st, u, _, _ := planUsageFixture(t)
	ctx := context.Background()

	quota := &model.TariffPlan{
		Slug: "usage-capped", Name: "Capped", PriceRub: 199, PeriodDays: 30,
		DataLimit: 10 << 30, Enabled: true,
	}
	if err := st.SaveTariffPlan(quota); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := m.ApplyPlanToUser(ctx, u.ID, quota.ID, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Spend the whole allowance: the user is now cut out of the generated config.
	addUsage(t, st, u.ID, 4<<30, 7<<30)
	before, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if before.UsedUp+before.UsedDown < before.DataLimit {
		t.Fatalf("setup: user is not over quota (%d/%d)", before.UsedUp+before.UsedDown, before.DataLimit)
	}

	// Renew the plan they are already on, the way a confirmed payment does.
	w, _, err := m.planWriteFor(*before, quota.ID, true, true)
	if err != nil {
		t.Fatalf("planWriteFor: %v", err)
	}
	if err := st.ApplyUserPlan(w); err != nil {
		t.Fatalf("apply plan write: %v", err)
	}

	after, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if used := after.UsedUp + after.UsedDown; used != 0 {
		t.Errorf("paid renewal left %d bytes on the counter — the user paid and stays blocked", used)
	}
	if after.ExpireAt <= before.ExpireAt {
		t.Errorf("renewal did not extend the period: %d → %d", before.ExpireAt, after.ExpireAt)
	}
}
