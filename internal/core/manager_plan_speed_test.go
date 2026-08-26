package core

import (
	"path/filepath"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
	"github.com/Shu1t3/rospanel-shu1t3/internal/xray"
)

// planSpeedManager is a manager over a fresh store, with the supervisor the plan
// paths reach for (a config apply is a no-op without an Xray binary).
func planSpeedManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "speed.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	return New(st, sup, xray.Options{}, TLSPaths{}, dir), st
}

// Editing a tariff's speed cap has to reach the people already on it. The other plan
// limits deliberately land only on (re)assignment — but a speed cap that ignores
// existing subscribers is the version an operator reports as "the limit doesn't
// work": they set 512 Kbps on the plan, watch nothing happen, and are right.
func TestPlanSpeedLimitReachesExistingSubscribers(t *testing.T) {
	m, st := planSpeedManager(t)

	plan := &model.TariffPlan{Name: "Standard", Slug: "standard", PriceRub: 100, PeriodDays: 30, Enabled: true}
	if err := m.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	u, err := m.CreateUser(t.Context(), "subscriber", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := m.ApplyPlanToUser(t.Context(), u.ID, plan.ID, false); err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	if fresh, _ := st.GetUser(u.ID); fresh.SpeedLimit != 0 {
		t.Fatalf("speed limit = %d before the plan had one", fresh.SpeedLimit)
	}

	plan.SpeedLimit = 512
	if err := m.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan with a cap: %v", err)
	}
	fresh, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fresh.SpeedLimit != 512 {
		t.Errorf("subscriber's speed limit = %d, want 512 from the plan", fresh.SpeedLimit)
	}

	// Lifting the plan's cap lifts it for its subscribers too — otherwise an
	// operator can only ever make a tariff slower.
	plan.SpeedLimit = 0
	if err := m.SaveTariffPlan(plan); err != nil {
		t.Fatalf("clear the cap: %v", err)
	}
	if fresh, _ := st.GetUser(u.ID); fresh.SpeedLimit != 0 {
		t.Errorf("speed limit = %d after the plan's cap was removed, want 0", fresh.SpeedLimit)
	}
}

// A user on a DIFFERENT plan must not be touched by an edit to this one.
func TestPlanSpeedLimitLeavesOtherPlansAlone(t *testing.T) {
	m, st := planSpeedManager(t)

	capped := &model.TariffPlan{Name: "Slow", Slug: "slow", PriceRub: 100, PeriodDays: 30, Enabled: true}
	other := &model.TariffPlan{Name: "Fast", Slug: "fast", PriceRub: 200, PeriodDays: 30, Enabled: true}
	for _, p := range []*model.TariffPlan{capped, other} {
		if err := m.SaveTariffPlan(p); err != nil {
			t.Fatalf("save plan: %v", err)
		}
	}
	u, err := m.CreateUser(t.Context(), "elsewhere", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := m.ApplyPlanToUser(t.Context(), u.ID, other.ID, false); err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	capped.SpeedLimit = 512
	if err := m.SaveTariffPlan(capped); err != nil {
		t.Fatalf("save: %v", err)
	}
	if fresh, _ := st.GetUser(u.ID); fresh.SpeedLimit != 0 {
		t.Errorf("a user on another plan was capped at %d", fresh.SpeedLimit)
	}
}
