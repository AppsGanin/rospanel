package core

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// TestPlanPriceFollowsDesignation holds the two halves of "free is a designation,
// not a price" together: a plan nobody designated cannot be saved for free, and
// designating one as the free/trial plan zeroes its price. Without the second
// half, a paid plan picked as the free one would be handed out for nothing and
// forever (registration and the expiry downgrade never charge) while IsFree still
// reported it paid — so it would also stay on sale in the user bot.
func TestPlanPriceFollowsDesignation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "price.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	// A plain plan must carry a price.
	if err := m.SaveTariffPlan(&model.TariffPlan{Slug: "std", Name: "Стандарт", PriceRub: 0, PeriodDays: 30}); err == nil {
		t.Fatal("saving an undesignated plan at price 0 must be rejected")
	}
	paid := &model.TariffPlan{Slug: "std", Name: "Стандарт", PriceRub: 199, PeriodDays: 30}
	if err := m.SaveTariffPlan(paid); err != nil {
		t.Fatalf("save paid: %v", err)
	}

	// Designating it as the free plan makes it free.
	set, _ := st.GetSettings()
	set.BillingEnabled = true
	set.BillingFreePlanID = paid.ID
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatalf("save billing settings: %v", err)
	}
	got, err := st.GetTariffPlan(paid.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.PriceRub != 0 {
		t.Fatalf("designated free plan price = %d, want 0", got.PriceRub)
	}
	if !got.IsFree() {
		t.Fatal("designated free plan must report IsFree")
	}

	// And editing it keeps it free even if a stale price rides in from the client.
	got.PriceRub = 500
	if err := m.SaveTariffPlan(got); err != nil {
		t.Fatalf("re-save designated plan: %v", err)
	}
	again, _ := st.GetTariffPlan(paid.ID)
	if again.PriceRub != 0 {
		t.Fatalf("designated plan price after edit = %d, want 0", again.PriceRub)
	}
}
