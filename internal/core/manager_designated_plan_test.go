package core

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// designatedFixture builds a manager with a free plan and a trial plan, both
// designated in the billing settings.
func designatedFixture(t *testing.T) (*Manager, *store.Store, *model.TariffPlan, *model.TariffPlan) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "designated.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st}

	free := &model.TariffPlan{Slug: "d-free", Name: "Free", PeriodDays: 30, DataLimit: 1 << 30, Enabled: true}
	trial := &model.TariffPlan{Slug: "d-trial", Name: "Trial", PeriodDays: 3, DataLimit: 5 << 30, Enabled: true}
	for _, p := range []*model.TariffPlan{free, trial} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save %s: %v", p.Slug, err)
		}
	}
	set, _ := st.GetSettings()
	set.BillingEnabled = true
	set.BillingFreePlanID = free.ID
	set.BillingTrialPlanID = trial.ID
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatalf("billing settings: %v", err)
	}
	return m, st, free, trial
}

// TestDesignatedPlanStaysActive: the editor no longer shows an "Активен" toggle for
// a designated free/trial plan, because such a plan is never offered for sale. The
// server must therefore keep the flag true — a stale false would drop the plan out
// of the admin's own plan pickers (they filter on enabled) with nothing on screen
// explaining why, and no control left to undo it.
func TestDesignatedPlanStaysActive(t *testing.T) {
	m, st, free, trial := designatedFixture(t)

	for _, p := range []*model.TariffPlan{free, trial} {
		p.Enabled = false
		p.PriceRub = 500 // and a price, which designation also has to override
		if err := m.SaveTariffPlan(p); err != nil {
			t.Fatalf("save %s: %v", p.Slug, err)
		}
		got, err := st.GetTariffPlan(p.ID)
		if err != nil {
			t.Fatalf("get %s: %v", p.Slug, err)
		}
		if !got.Enabled {
			t.Errorf("%s: designated plan saved as disabled — it would vanish from the "+
				"plan pickers with no toggle left to restore it", p.Slug)
		}
		if got.PriceRub != 0 {
			t.Errorf("%s: designated plan kept price %d, want 0", p.Slug, got.PriceRub)
		}
	}
}

// TestDesignatingNormalizesExistingPlan covers the other direction: a plan that is
// already disabled (or priced) when it gets designated must be normalized too, not
// just one saved through the plan editor afterwards.
func TestDesignatingNormalizesExistingPlan(t *testing.T) {
	m, st, _, _ := designatedFixture(t)

	stale := &model.TariffPlan{Slug: "stale", Name: "Stale", PriceRub: 300, PeriodDays: 7, Enabled: false}
	if err := st.SaveTariffPlan(stale); err != nil {
		t.Fatalf("save: %v", err)
	}
	set, _ := st.GetSettings()
	set.BillingFreePlanID = stale.ID
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatalf("designate: %v", err)
	}

	got, err := st.GetTariffPlan(stale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Enabled || got.PriceRub != 0 {
		t.Fatalf("designated plan not normalized: enabled=%v price=%d", got.Enabled, got.PriceRub)
	}
}

// TestTrialGrantedRegardlessOfEnabledFlag: with the toggle gone from the editor,
// plan.Enabled must not still gate the registration trial. A database carrying a
// legacy enabled=0 on the trial plan would otherwise silently hand out no trials,
// with no control in the UI to explain or fix it.
func TestTrialGrantedRegardlessOfEnabledFlag(t *testing.T) {
	m, st, _, trial := designatedFixture(t)

	// Write the legacy state through the store directly: only the manager's
	// SaveTariffPlan normalizes designated plans, so this bypasses it.
	trial.Enabled = false
	if err := st.SaveTariffPlan(trial); err != nil {
		t.Fatalf("force disabled: %v", err)
	}
	got, _ := st.GetTariffPlan(trial.ID)
	if got.Enabled {
		t.Fatal("precondition failed: trial plan should be disabled for this test")
	}

	u, err := m.createRegisteredUser("newcomer")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if u.PlanID != trial.ID {
		t.Fatalf("registered on plan %d, want the trial plan %d — a stale enabled=0 "+
			"still switches trials off", u.PlanID, trial.ID)
	}
	if u.ExpireAt == 0 {
		t.Error("trial granted without an expiry")
	}
}
