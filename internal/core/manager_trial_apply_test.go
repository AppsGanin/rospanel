package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// TestApplyPlanTrialVsFree checks that a zero-price plan designated as the trial
// plan EXPIRES when assigned manually (period-limited, reset "none"), while the
// free plan never expires and refills its quota on a rolling "days:N" cycle.
func TestApplyPlanTrialVsFree(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "trial.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	free := &model.TariffPlan{Slug: "tf-free", Name: "Free", PriceRub: 0, PeriodDays: 30, DataLimit: 1 << 30, Enabled: true}
	trial := &model.TariffPlan{Slug: "tf-trial", Name: "Trial", PriceRub: 0, PeriodDays: 3, DataLimit: 5 << 30, Enabled: true}
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

	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Manually assign the TRIAL plan → must expire, reset "none".
	if err := m.ApplyPlanToUser(context.Background(), u.ID, trial.ID, false); err != nil {
		t.Fatalf("apply trial: %v", err)
	}
	cur, _ := st.GetUser(u.ID)
	if cur.ExpireAt == 0 {
		t.Fatalf("trial plan must set an expiry, got expire_at=0")
	}
	if cur.ResetPeriod != "none" {
		t.Fatalf("trial reset_period = %q, want none", cur.ResetPeriod)
	}
	if m.ActivePaidPlan(*cur) != nil {
		t.Fatalf("trial (price 0) must not count as an active PAID plan")
	}

	// Manually assign the FREE plan → never expires, rolling days:30 reset.
	if err := m.ApplyPlanToUser(context.Background(), u.ID, free.ID, false); err != nil {
		t.Fatalf("apply free: %v", err)
	}
	cur, _ = st.GetUser(u.ID)
	if cur.ExpireAt != 0 {
		t.Fatalf("free plan must never expire, got expire_at=%d", cur.ExpireAt)
	}
	if !strings.HasPrefix(cur.ResetPeriod, "days:") {
		t.Fatalf("free reset_period = %q, want days:N", cur.ResetPeriod)
	}
	if cur.ResetPeriod != "days:30" {
		t.Fatalf("free reset_period = %q, want days:30 (from PeriodDays)", cur.ResetPeriod)
	}
}
