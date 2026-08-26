package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

// A plan's access groups are the point of the feature: assigning the plan must move
// the user's ACCESS, not just their quota — the same resolved Access that config
// generation reads to decide which inbounds carry the user's credential.
func TestPlanGroupsGateAccess(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "plangroups.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	vlessOnly, err := st.CreateGroup("VLESS only", []string{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS)})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	all, err := st.CreateGroup("Everything", []string{
		model.BuiltinToken(model.LocalNodeID, model.LaneVLESS),
		model.BuiltinToken(model.LocalNodeID, model.LaneHysteria),
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	cheap := &model.TariffPlan{Slug: "cheap", Name: "Cheap", PriceRub: 100, PeriodDays: 30, Enabled: true, GroupIDs: []int64{vlessOnly.ID}}
	rich := &model.TariffPlan{Slug: "rich", Name: "Rich", PriceRub: 500, PeriodDays: 30, Enabled: true, GroupIDs: []int64{all.ID}}
	for _, p := range []*model.TariffPlan{cheap, rich} {
		if err := m.SaveTariffPlan(p); err != nil {
			t.Fatalf("save %s: %v", p.Slug, err)
		}
	}

	u, err := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	// No plan, no groups: unrestricted, exactly as before this feature existed.
	if acc, _ := st.UserAccess(u.ID); !acc.All {
		t.Fatal("a user on no plan and in no group must reach everything")
	}

	ctx := context.Background()
	if err := m.ApplyPlanToUser(ctx, u.ID, cheap.ID, false); err != nil {
		t.Fatalf("apply cheap: %v", err)
	}
	acc, _ := st.UserAccess(u.ID)
	if acc.All ||
		!acc.Tokens[model.BuiltinToken(model.LocalNodeID, model.LaneVLESS)] ||
		acc.Tokens[model.BuiltinToken(model.LocalNodeID, model.LaneHysteria)] {
		t.Fatalf("cheap plan access = %+v, want VLESS only", acc)
	}

	if err := m.ApplyPlanToUser(ctx, u.ID, rich.ID, false); err != nil {
		t.Fatalf("apply rich: %v", err)
	}
	acc, _ = st.UserAccess(u.ID)
	if !acc.Tokens[model.BuiltinToken(model.LocalNodeID, model.LaneHysteria)] {
		t.Fatalf("upgrading did not grant the new plan's lane: %+v", acc)
	}
	groups, _ := st.GroupsForUser(u.ID)
	if len(groups) != 1 || groups[0].ID != all.ID {
		t.Fatalf("groups after the upgrade = %+v, want only %q", groups, all.Name)
	}

	// Back to manual mode (plan 0): the plan's groups go with it, and an ungrouped
	// user is unrestricted again.
	if err := m.ApplyPlanToUser(ctx, u.ID, 0, false); err != nil {
		t.Fatalf("clear plan: %v", err)
	}
	if acc, _ = st.UserAccess(u.ID); !acc.All {
		t.Fatalf("leaving every plan left the user gated: %+v", acc)
	}
}

// Every group change costs an Xray reconcile, so the detector must fire on real
// movement only. The trap it guards: a user whose HAND-assigned group the plan also
// grants keeps that row as manual forever (the plan never takes ownership), so a
// naive "plan-owned set vs granted set" comparison would report a change on every
// single renewal and restart Xray for nothing.
func TestPlanGroupsChangedOnlyOnRealMovement(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "changed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	g, err := st.CreateGroup("Premium", []string{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS)})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	other, _ := st.CreateGroup("Other", nil)
	u, _ := st.CreateUser("u", "uuid", "pw", "tok", 0, 0, 0)

	w := store.UserPlanWrite{UserID: u.ID, PlanID: 1, GroupIDs: []int64{g.ID}, ResetPeriod: "none"}
	if !m.planGroupsChanged(w) {
		t.Fatal("granting a group the user is not in must count as a change")
	}
	// The operator put them in that group by hand: applying the plan adds no row.
	if err := st.SetUserGroups(u.ID, []int64{g.ID}); err != nil {
		t.Fatalf("set groups: %v", err)
	}
	if m.planGroupsChanged(w) {
		t.Fatal("the user is already in the granted group by hand — nothing moves, so no reconcile")
	}
	// Same user, a plan that grants something else: that one really is a change.
	w2 := w
	w2.GroupIDs = []int64{other.ID}
	if !m.planGroupsChanged(w2) {
		t.Fatal("granting a different group must count as a change")
	}
	// And a plan-owned membership that the next plan doesn't grant is a change too.
	if err := st.ApplyUserPlan(store.UserPlanWrite{UserID: u.ID, GroupIDs: []int64{other.ID}, ResetPeriod: "none"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !m.planGroupsChanged(store.UserPlanWrite{UserID: u.ID, ResetPeriod: "none"}) {
		t.Fatal("taking back a plan-owned group must count as a change")
	}
}

// A group deleted while the plan editor was open must not make the plan unsavable —
// the id is dropped instead (the FK would refuse the write otherwise).
func TestSaveTariffPlanDropsUnknownGroups(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "plangroups2.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	g, err := st.CreateGroup("Real", nil)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	p := &model.TariffPlan{
		Slug: "p", Name: "P", PriceRub: 100, PeriodDays: 30, Enabled: true,
		GroupIDs: []int64{g.ID, 4242, g.ID}, // one real, one deleted, one duplicate
	}
	if err := m.SaveTariffPlan(p); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	got, err := st.GetTariffPlan(p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if len(got.GroupIDs) != 1 || got.GroupIDs[0] != g.ID {
		t.Fatalf("plan groups = %v, want just [%d]", got.GroupIDs, g.ID)
	}
}
