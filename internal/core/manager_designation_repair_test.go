package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// TestDesignatingPaidPlanRewritesItsUsers covers a trap in the designation flow.
// Zeroing the plan's price is not enough: everyone already on it still carries the
// shape they bought it under — an expiry in the future and no refill cycle. Left
// that way they go expired and are then stuck forever, because EnforceBilling skips
// users already on the free plan and a free plan cannot be renewed.
func TestDesignatingPaidPlanRewritesItsUsers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "designate.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}
	ctx := context.Background()

	paid := &model.TariffPlan{Slug: "std", Name: "Стандарт", PriceRub: 300, PeriodDays: 30,
		DataLimit: 50 << 30, Enabled: true}
	if err := st.SaveTariffPlan(paid); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	u, err := st.CreateUser("bob", "uuid-bob", "pw", "tok-bob", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	// Bob buys the paid plan: expiry in the future, no reset cycle.
	if err := m.ApplyPlanToUser(ctx, u.ID, paid.ID, false); err != nil {
		t.Fatalf("buy: %v", err)
	}
	before, _ := st.GetUser(u.ID)
	if before.ExpireAt == 0 {
		t.Fatal("precondition: a paid plan must set an expiry")
	}

	// The operator now designates that same plan as the free one.
	set, _ := st.GetSettings()
	set.BillingEnabled = true
	set.BillingFreePlanID = paid.ID
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatalf("designate: %v", err)
	}

	after, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.ExpireAt != 0 {
		t.Fatalf("user still expires at %d after their plan became the free one — "+
			"they will go expired and nothing can rescue or renew them", after.ExpireAt)
	}
	if after.ResetPeriod == "none" {
		t.Errorf("free plan user has no refill cycle (reset_period=%q); their quota "+
			"is one-shot forever", after.ResetPeriod)
	}
	if after.PlanID != paid.ID {
		t.Errorf("plan link changed to %d, want %d", after.PlanID, paid.ID)
	}

	// And the sweep that downgrades expired users leaves them alone, as it should —
	// there is nothing left to fix.
	if err := m.EnforceBilling(time.Now().Add(400 * 24 * time.Hour).Unix()); err != nil {
		t.Fatalf("enforce: %v", err)
	}
	final, _ := st.GetUser(u.ID)
	if final.ExpireAt != 0 {
		t.Errorf("expiry reappeared after the sweep: %d", final.ExpireAt)
	}
}

// TestFreeAndTrialMustDiffer: pointing both roles at one plan is a dead end for
// every self-registered user — the trial expires and EnforceBilling will not
// downgrade someone already on the free plan. Refuse the configuration instead of
// letting the operator discover it a trial period later.
func TestFreeAndTrialMustDiffer(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "same.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	plan := &model.TariffPlan{Slug: "only-one", Name: "Единственный", PeriodDays: 7, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save: %v", err)
	}
	set, _ := st.GetSettings()
	set.BillingEnabled = true
	set.BillingFreePlanID = plan.ID
	set.BillingTrialPlanID = plan.ID

	if err := m.SaveBillingSettings(set); err == nil {
		t.Fatal("one plan accepted for both roles — every registrant would be stranded " +
			"when their trial ends")
	}

	// Distinct plans are of course fine.
	other := &model.TariffPlan{Slug: "second-one", Name: "Пробный", PeriodDays: 3, Enabled: true}
	if err := st.SaveTariffPlan(other); err != nil {
		t.Fatalf("save other: %v", err)
	}
	set.BillingTrialPlanID = other.ID
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatalf("distinct plans rejected: %v", err)
	}
}
