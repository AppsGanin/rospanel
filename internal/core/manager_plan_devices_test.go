package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

// TestPlanDeviceLimitValidAndMax tests that any arbitrary device limit from 0 up to
// model.MaxDevicesPerUser is accepted and stored correctly, while values < 0 or
// > model.MaxDevicesPerUser are rejected.
func TestPlanDeviceLimitValidAndMax(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "devlimit.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	validLimits := []int{0, 1, 2, 7, 15, 25, 49, model.MaxDevicesPerUser}
	for i, lim := range validLimits {
		plan := &model.TariffPlan{
			Slug:        fmt.Sprintf("plan-dev-%d", i),
			Name:        fmt.Sprintf("Test Plan %d", i),
			PriceRub:    100,
			PeriodDays:  30,
			DeviceLimit: lim,
		}
		if err := m.SaveTariffPlan(plan); err != nil {
			t.Fatalf("saving plan with device_limit=%d failed: %v", lim, err)
		}
		got, err := st.GetTariffPlan(plan.ID)
		if err != nil {
			t.Fatalf("get plan %d: %v", plan.ID, err)
		}
		if got.DeviceLimit != lim {
			t.Errorf("plan %d DeviceLimit = %d, want %d", plan.ID, got.DeviceLimit, lim)
		}
	}

	// Negative device limit must be rejected.
	negPlan := &model.TariffPlan{
		Slug:        "plan-neg",
		Name:        "Neg Plan",
		PriceRub:    100,
		PeriodDays:  30,
		DeviceLimit: -1,
	}
	if err := m.SaveTariffPlan(negPlan); err == nil {
		t.Fatal("saving plan with negative device_limit must be rejected")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Code != "err.planLimitsNegative" {
			t.Errorf("got error %v, want code err.planLimitsNegative", err)
		}
	}

	// Device limit exceeding MaxDevicesPerUser must be rejected.
	overPlan := &model.TariffPlan{
		Slug:        "plan-over",
		Name:        "Over Plan",
		PriceRub:    100,
		PeriodDays:  30,
		DeviceLimit: model.MaxDevicesPerUser + 1,
	}
	if err := m.SaveTariffPlan(overPlan); err == nil {
		t.Fatal("saving plan with device_limit > MaxDevicesPerUser must be rejected")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Code != "err.planDeviceLimitRange" {
			t.Errorf("got error %v, want code err.planDeviceLimitRange", err)
		}
	}
}
