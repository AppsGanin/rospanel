package core

import (
	"context"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// planResetFixture is a manager over a fresh store with billing on, a user, and the
// seeded free plan given a duration and a quota so its derived days:N cycle exists.
func planResetFixture(t *testing.T) (*Manager, *store.Store, model.User, *model.TariffPlan) {
	t.Helper()
	m, st := planSpeedManager(t)
	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	free, err := st.GetTariffPlan(set.BillingFreePlanID)
	if err != nil {
		t.Fatalf("free plan: %v", err)
	}
	free.PeriodDays, free.DataLimit = 30, 1<<30
	if err := st.SaveTariffPlan(free); err != nil {
		t.Fatalf("save free: %v", err)
	}
	set.BillingEnabled = true
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatalf("billing settings: %v", err)
	}
	u, err := m.CreateUser(t.Context(), "cyclist", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return m, st, *u, free
}

func resetPeriodOf(t *testing.T, st *store.Store, id int64) string {
	t.Helper()
	u, err := st.GetUser(id)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	return u.ResetPeriod
}

// "100 GB a month, paid for a year" is the plan this field exists for. Assigning
// the plan gives the user its calendar cycle; without the field a paid plan runs
// its quota over the whole period (the derived default, kept for every existing plan).
func TestPlanResetPeriodLandsOnAssignment(t *testing.T) {
	m, st, u, _ := planResetFixture(t)
	ctx := context.Background()

	yearly := &model.TariffPlan{
		Name: "Yearly", Slug: "yearly", PriceRub: 1200, PeriodDays: 365,
		DataLimit: 100 << 30, ResetPeriod: "monthly", Enabled: true,
	}
	plain := &model.TariffPlan{
		Name: "Plain", Slug: "plain", PriceRub: 200, PeriodDays: 30,
		DataLimit: 100 << 30, Enabled: true,
	}
	for _, p := range []*model.TariffPlan{yearly, plain} {
		if err := m.SaveTariffPlan(p); err != nil {
			t.Fatalf("save %s: %v", p.Slug, err)
		}
	}
	if err := m.ApplyPlanToUser(ctx, u.ID, yearly.ID, false); err != nil {
		t.Fatalf("apply yearly: %v", err)
	}
	if got := resetPeriodOf(t, st, u.ID); got != "monthly" {
		t.Errorf("reset_period = %q after the monthly plan, want monthly", got)
	}
	// Moving to a plan without a cycle takes the cycle away — the plan decides.
	if err := m.ApplyPlanToUser(ctx, u.ID, plain.ID, false); err != nil {
		t.Fatalf("apply plain: %v", err)
	}
	if got := resetPeriodOf(t, st, u.ID); got != "none" {
		t.Errorf("reset_period = %q after a plan with no cycle, want none", got)
	}
}

// A purchase of a plan that refills on its own still hands over a fresh counter:
// money taken must mean access now, not on the 1st. The cycle survives the write.
func TestPlanResetPeriodPurchaseRefillsAndKeepsCycle(t *testing.T) {
	m, st, u, _ := planResetFixture(t)
	ctx := context.Background()

	plan := &model.TariffPlan{
		Name: "Monthly", Slug: "monthly", PriceRub: 1200, PeriodDays: 365,
		DataLimit: 10 << 30, ResetPeriod: "monthly", Enabled: true,
	}
	if err := m.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := m.ApplyPlanToUser(ctx, u.ID, plan.ID, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	addUsage(t, st, u.ID, 4<<30, 7<<30)
	before, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	w, _, err := m.planWriteFor(*before, plan.ID, true, true)
	if err != nil {
		t.Fatalf("planWriteFor: %v", err)
	}
	if err := st.ApplyUserPlan(w); err != nil {
		t.Fatalf("apply write: %v", err)
	}
	after, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if used := after.UsedUp + after.UsedDown; used != 0 {
		t.Errorf("purchase left %d bytes on the counter", used)
	}
	if after.ResetPeriod != "monthly" {
		t.Errorf("purchase rewrote reset_period to %q", after.ResetPeriod)
	}
}

// An explicit cycle on the FREE plan replaces its derived rolling days:N; clearing
// it brings days:N back. Both reach the people already on the plan, the way the
// speed cap does — a tariff edit that does nothing is the one reported as broken.
func TestPlanResetPeriodExplicitBeatsFreeDefaultAndIsRetroactive(t *testing.T) {
	m, st, u, free := planResetFixture(t)
	ctx := context.Background()

	if err := m.ApplyPlanToUser(ctx, u.ID, free.ID, false); err != nil {
		t.Fatalf("apply free: %v", err)
	}
	if got := resetPeriodOf(t, st, u.ID); got != "days:30" {
		t.Fatalf("free plan reset_period = %q, want the derived days:30", got)
	}

	free.ResetPeriod = "weekly"
	if err := m.SaveTariffPlan(free); err != nil {
		t.Fatalf("save weekly: %v", err)
	}
	if got := resetPeriodOf(t, st, u.ID); got != "weekly" {
		t.Errorf("reset_period = %q after the plan went weekly, want weekly", got)
	}
	fresh, err := st.GetTariffPlan(free.ID)
	if err != nil || fresh.ResetPeriod != "weekly" {
		t.Fatalf("plan reset_period = %q (%v), want weekly", fresh.ResetPeriod, err)
	}

	// "none" is the API's spelling of blank; both mean the derived default.
	free.ResetPeriod = "none"
	if err := m.SaveTariffPlan(free); err != nil {
		t.Fatalf("save none: %v", err)
	}
	if free.ResetPeriod != "" {
		t.Errorf("none was stored as %q, want blank", free.ResetPeriod)
	}
	if got := resetPeriodOf(t, st, u.ID); got != "days:30" {
		t.Errorf("reset_period = %q after clearing the cycle, want days:30 again", got)
	}
}

// A user on a DIFFERENT plan is not touched by an edit to this one, and a plan
// with no quota carries no cycle at all — there is nothing to refill.
func TestPlanResetPeriodScope(t *testing.T) {
	m, st, u, _ := planResetFixture(t)
	ctx := context.Background()

	unlimited := &model.TariffPlan{
		Name: "Unlimited", Slug: "unlimited", PriceRub: 500, PeriodDays: 30,
		ResetPeriod: "daily", Enabled: true,
	}
	other := &model.TariffPlan{
		Name: "Other", Slug: "other", PriceRub: 300, PeriodDays: 30, DataLimit: 5 << 30, Enabled: true,
	}
	for _, p := range []*model.TariffPlan{unlimited, other} {
		if err := m.SaveTariffPlan(p); err != nil {
			t.Fatalf("save %s: %v", p.Slug, err)
		}
	}
	if err := m.ApplyPlanToUser(ctx, u.ID, unlimited.ID, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := resetPeriodOf(t, st, u.ID); got != "none" {
		t.Errorf("an unlimited plan gave reset_period %q, want none", got)
	}
	other.ResetPeriod = "monthly"
	if err := m.SaveTariffPlan(other); err != nil {
		t.Fatalf("save other: %v", err)
	}
	if got := resetPeriodOf(t, st, u.ID); got != "none" {
		t.Errorf("an edit to another plan changed reset_period to %q", got)
	}
}

// The field reaches SaveTariffPlan straight off the API's JSON decode, so a value
// the quota sweep would never act on has to be refused, not stored.
func TestPlanResetPeriodRejectsUnknown(t *testing.T) {
	m, _ := planSpeedManager(t)
	for _, bad := range []string{"hourly", "days:30", "Monthly"} {
		p := &model.TariffPlan{Name: "Bad", Slug: "bad", PriceRub: 100, PeriodDays: 30, DataLimit: 1 << 30, ResetPeriod: bad, Enabled: true}
		if err := m.SaveTariffPlan(p); err == nil {
			t.Errorf("reset_period %q was accepted", bad)
		}
	}
}
