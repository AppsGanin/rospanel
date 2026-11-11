package store

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A plan writes its HWID limit; bought devices go onto both limits and come off both.
func TestPlanWritesTheHWIDLimitToo(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	u, err := st.CreateUser("buyer", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserHWIDLimit(u.ID, model.HWIDLimitUnlimited); err != nil {
		t.Fatal(err)
	}
	plan := &model.TariffPlan{Slug: "p", Name: "P", PriceRub: 100, PeriodDays: 30, DeviceLimit: 2, HWIDLimit: 2, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	w := planWrite(u.ID, plan)
	w.DeviceLimit, w.HWIDLimit = plan.DeviceLimit, plan.HWIDLimit
	if err := st.ApplyUserPlan(w); err != nil {
		t.Fatal(err)
	}
	limits := func() (int, int) {
		t.Helper()
		g, err := st.GetUser(u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return g.DeviceLimit, g.HWIDLimit
	}
	if d, h := limits(); d != 2 || h != 2 {
		t.Fatalf("after the plan: device %d hwid %d, want 2 and 2", d, h)
	}
	if err := applyAddonOn(st.db, AddonWrite{UserID: u.ID, PlanID: plan.ID, AddDevices: 3, MaxDevices: 5,
		RequireExtra: -1, RequirePack: -1}); err != nil {
		t.Fatal(err)
	}
	if d, h := limits(); d != 5 || h != 5 {
		t.Fatalf("after the add-on: device %d hwid %d, want 5 and 5", d, h)
	}
	if ok, err := st.TakeBackAddon(u.ID, plan.ID, 3, 0); err != nil || !ok {
		t.Fatalf("take back: %v %v", ok, err)
	}
	if d, h := limits(); d != 2 || h != 2 {
		t.Fatalf("after the take-back: device %d hwid %d, want 2 and 2", d, h)
	}
}

// A new user starts with the HWID limit equal to the address limit, as the one
// number used to be.
func TestCreateUserSeedsTheHWIDLimit(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	u, err := st.createUser("n", "uuid", "pw", "tok", 0, 0, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if u.HWIDLimit != 4 {
		t.Fatalf("hwid_limit %d, want 4", u.HWIDLimit)
	}
}

// The plan's two limits land apart, and survive a save and a read.
func TestPlanHWIDLimitIsItsOwn(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan := &model.TariffPlan{Slug: "s", Name: "S", PriceRub: 100, PeriodDays: 30, DeviceLimit: 3,
		HWIDLimit: model.HWIDLimitUnlimited, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	read, err := st.GetTariffPlan(plan.ID)
	if err != nil || read.HWIDLimit != model.HWIDLimitUnlimited {
		t.Fatalf("read back: %+v %v", read, err)
	}
	w := planWrite(u.ID, read)
	w.DeviceLimit, w.HWIDLimit = read.DeviceLimit, read.HWIDLimit
	if err := st.ApplyUserPlan(w); err != nil {
		t.Fatal(err)
	}
	g, err := st.GetUser(u.ID)
	if err != nil || g.DeviceLimit != 3 || g.HWIDLimit != model.HWIDLimitUnlimited {
		t.Fatalf("user: device %d hwid %d (%v), want 3 and -1", g.DeviceLimit, g.HWIDLimit, err)
	}
}

// Devices bought on a plan whose HWID limit is the panel default go onto that
// default as a number, and come off it again.
func TestAddonRaisesADefaultHWIDLimit(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	set.HWIDFallbackLimit = 1
	if err := st.SetHWIDSettings(set); err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser("d", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan := &model.TariffPlan{Slug: "d", Name: "D", PriceRub: 100, PeriodDays: 30, DeviceLimit: 2, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	w := planWrite(u.ID, plan)
	w.DeviceLimit = plan.DeviceLimit // HWID stays 0, the default
	if err := st.ApplyUserPlan(w); err != nil {
		t.Fatal(err)
	}
	if err := applyAddonOn(st.db, AddonWrite{UserID: u.ID, PlanID: plan.ID, AddDevices: 3, MaxDevices: 5,
		RequireExtra: -1, RequirePack: -1}); err != nil {
		t.Fatal(err)
	}
	if g, _ := st.GetUser(u.ID); g.HWIDLimit != 4 {
		t.Fatalf("hwid after the add-on %d, want the default 1 + 3", g.HWIDLimit)
	}
	if ok, err := st.TakeBackAddon(u.ID, plan.ID, 3, 0); err != nil || !ok {
		t.Fatalf("take back: %v %v", ok, err)
	}
	if g, _ := st.GetUser(u.ID); g.HWIDLimit != 1 {
		t.Fatalf("hwid after the take-back %d, want 1", g.HWIDLimit)
	}
}
