package store

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// groupNamesOf is the assertion helper: which groups the user is in, and which of
// those the plan owns.
func groupNamesOf(t *testing.T, st *Store, userID int64) (all []string, viaPlan []string) {
	t.Helper()
	rows, err := st.db.Query(`
		SELECT g.name, m.via_plan FROM group_members m
		JOIN groups g ON g.id = m.group_id
		WHERE m.user_id = ? ORDER BY g.name`, userID)
	if err != nil {
		t.Fatalf("read membership: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var via int
		if err := rows.Scan(&name, &via); err != nil {
			t.Fatalf("scan: %v", err)
		}
		all = append(all, name)
		if via != 0 {
			viaPlan = append(viaPlan, name)
		}
	}
	return all, viaPlan
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// planWrite is a minimal assignment of a plan to a user.
func planWrite(userID int64, p *model.TariffPlan) UserPlanWrite {
	now := time.Now().Unix()
	return UserPlanWrite{
		UserID: userID, PlanID: p.ID, GroupIDs: p.GroupIDs,
		ResetPeriod: "none", ResetAnchor: now,
	}
}

// A plan grants its groups on assignment and takes them back when the user moves to
// another plan — otherwise a user who has been through several tariffs accumulates
// every group, and access being the UNION of them, the gate stops gating.
func TestPlanGroupsFollowThePlan(t *testing.T) {
	st := newStore(t)
	u, err := st.CreateUser("buyer", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	basic, err := st.CreateGroup("Basic", []string{model.BuiltinToken(0, model.LaneVLESS)})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	premium, err := st.CreateGroup("Premium", []string{model.BuiltinToken(0, model.LaneHysteria)})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	planA := &model.TariffPlan{Slug: "a", Name: "A", PriceRub: 100, PeriodDays: 30, Enabled: true, GroupIDs: []int64{basic.ID}}
	planB := &model.TariffPlan{Slug: "b", Name: "B", PriceRub: 300, PeriodDays: 30, Enabled: true, GroupIDs: []int64{premium.ID}}
	for _, p := range []*model.TariffPlan{planA, planB} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save plan: %v", err)
		}
	}

	if err := st.ApplyUserPlan(planWrite(u.ID, planA)); err != nil {
		t.Fatalf("apply A: %v", err)
	}
	all, via := groupNamesOf(t, st, u.ID)
	if !eq(all, []string{"Basic"}) || !eq(via, []string{"Basic"}) {
		t.Fatalf("after plan A: groups=%v viaPlan=%v, want [Basic] both", all, via)
	}

	if err := st.ApplyUserPlan(planWrite(u.ID, planB)); err != nil {
		t.Fatalf("apply B: %v", err)
	}
	all, via = groupNamesOf(t, st, u.ID)
	if !eq(all, []string{"Premium"}) || !eq(via, []string{"Premium"}) {
		t.Fatalf("after plan B: groups=%v viaPlan=%v, want [Premium] both — "+
			"the previous plan's group must be taken back", all, via)
	}

	// Off every plan (manual mode / cancellation with no free plan): nothing left.
	if err := st.ApplyUserPlan(UserPlanWrite{UserID: u.ID, ResetPeriod: "none"}); err != nil {
		t.Fatalf("clear plan: %v", err)
	}
	if all, _ = groupNamesOf(t, st, u.ID); len(all) != 0 {
		t.Fatalf("moving off every plan left groups behind: %v", all)
	}
}

// A group an operator assigned by hand is theirs, not the tariff's: a plan switch —
// or a payment landing at 3am — must not undo it. That is what via_plan is for.
func TestManualGroupsSurvivePlanChanges(t *testing.T) {
	st := newStore(t)
	u, _ := st.CreateUser("buyer", "uuid", "pw", "tok", 0, 0, 0)
	testers, _ := st.CreateGroup("Testers", []string{model.BuiltinToken(0, model.LaneReality)})
	premium, _ := st.CreateGroup("Premium", []string{model.BuiltinToken(0, model.LaneHysteria)})

	// By hand: the operator puts them in Testers AND in Premium.
	if err := st.SetUserGroups(u.ID, []int64{testers.ID, premium.ID}); err != nil {
		t.Fatalf("set groups: %v", err)
	}

	plan := &model.TariffPlan{Slug: "p", Name: "P", PriceRub: 100, PeriodDays: 30, Enabled: true, GroupIDs: []int64{premium.ID}}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := st.ApplyUserPlan(planWrite(u.ID, plan)); err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	// Premium was already theirs by hand, so the plan does NOT take ownership of it.
	all, via := groupNamesOf(t, st, u.ID)
	if !eq(all, []string{"Premium", "Testers"}) || len(via) != 0 {
		t.Fatalf("after the plan: groups=%v viaPlan=%v, want both groups and none owned by the plan", all, via)
	}

	// Moving off the plan keeps everything the operator chose.
	if err := st.ApplyUserPlan(UserPlanWrite{UserID: u.ID, ResetPeriod: "none"}); err != nil {
		t.Fatalf("clear plan: %v", err)
	}
	all, _ = groupNamesOf(t, st, u.ID)
	if !eq(all, []string{"Premium", "Testers"}) {
		t.Fatalf("a plan write took back hand-assigned groups: %v", all)
	}
}

// Editing a user's groups in the card must not launder the plan's own grants into
// manual ones — the next plan switch could then never take them back.
func TestUserGroupEditKeepsPlanOwnership(t *testing.T) {
	st := newStore(t)
	u, _ := st.CreateUser("buyer", "uuid", "pw", "tok", 0, 0, 0)
	premium, _ := st.CreateGroup("Premium", []string{model.BuiltinToken(0, model.LaneHysteria)})
	extra, _ := st.CreateGroup("Extra", nil)

	plan := &model.TariffPlan{Slug: "p", Name: "P", PriceRub: 100, PeriodDays: 30, Enabled: true, GroupIDs: []int64{premium.ID}}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := st.ApplyUserPlan(planWrite(u.ID, plan)); err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	// The operator adds a second group by hand, keeping the plan's one ticked.
	if err := st.SetUserGroups(u.ID, []int64{premium.ID, extra.ID}); err != nil {
		t.Fatalf("set groups: %v", err)
	}
	all, via := groupNamesOf(t, st, u.ID)
	if !eq(all, []string{"Extra", "Premium"}) || !eq(via, []string{"Premium"}) {
		t.Fatalf("groups=%v viaPlan=%v, want Premium still owned by the plan", all, via)
	}

	// Off the plan: the plan's group goes, the hand-added one stays.
	if err := st.ApplyUserPlan(UserPlanWrite{UserID: u.ID, ResetPeriod: "none"}); err != nil {
		t.Fatalf("clear plan: %v", err)
	}
	all, _ = groupNamesOf(t, st, u.ID)
	if !eq(all, []string{"Extra"}) {
		t.Fatalf("after leaving the plan: %v, want [Extra]", all)
	}
}

// The group-side member editor is the twin of the user card and must keep the flag
// just the same — otherwise moving one user in the group screen would launder every
// plan grant in it into a manual one.
func TestGroupMemberEditKeepsPlanOwnership(t *testing.T) {
	st := newStore(t)
	u, _ := st.CreateUser("buyer", "uuid", "pw", "tok", 0, 0, 0)
	other, _ := st.CreateUser("second", "uuid2", "pw", "tok2", 0, 0, 0)
	premium, _ := st.CreateGroup("Premium", []string{model.BuiltinToken(0, model.LaneHysteria)})

	plan := &model.TariffPlan{Slug: "p", Name: "P", PriceRub: 100, PeriodDays: 30, Enabled: true, GroupIDs: []int64{premium.ID}}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := st.ApplyUserPlan(planWrite(u.ID, plan)); err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	// The operator adds a second user to the group from the group screen.
	if err := st.SetGroupMembers(premium.ID, []int64{u.ID, other.ID}); err != nil {
		t.Fatalf("set members: %v", err)
	}
	if _, via := groupNamesOf(t, st, u.ID); !eq(via, []string{"Premium"}) {
		t.Fatalf("the buyer's membership stopped being the plan's: viaPlan=%v", via)
	}
	if _, via := groupNamesOf(t, st, other.ID); len(via) != 0 {
		t.Fatalf("a hand-added member was marked as plan-granted: viaPlan=%v", via)
	}
}

// If saving a plan rolls back, the caller's struct must not keep the id the aborted
// INSERT handed out: the row does not exist, so a retry with that id would UPDATE
// nothing and look like a success.
func TestSaveTariffPlanRollbackClearsTheID(t *testing.T) {
	st := newStore(t)
	g, _ := st.CreateGroup("G", nil)
	if _, err := st.db.Exec(
		`CREATE TRIGGER t_fail_plan_groups BEFORE INSERT ON plan_groups
		 BEGIN SELECT RAISE(ABORT, 'simulated crash'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}
	p := &model.TariffPlan{Slug: "x", Name: "X", PriceRub: 100, PeriodDays: 30, Enabled: true, GroupIDs: []int64{g.ID}}
	if err := st.SaveTariffPlan(p); err == nil {
		t.Fatal("expected the save to fail")
	}
	if p.ID != 0 {
		t.Fatalf("plan id = %d after a rolled-back create, want 0", p.ID)
	}
	plans, err := st.ListTariffPlans(true) // the seed plans from the migrations, and only those
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, got := range plans {
		if got.Slug == p.Slug {
			t.Fatalf("a rolled-back create left a plan behind: %+v", got)
		}
	}
}

// Adding a group to a plan means "this tariff includes that connection", so the users
// already on it move too — a change that only applied to future buyers would silently
// split one tariff into two.
func TestEditingAPlanMovesItsUsers(t *testing.T) {
	st := newStore(t)
	u, _ := st.CreateUser("buyer", "uuid", "pw", "tok", 0, 0, 0)
	basic, _ := st.CreateGroup("Basic", []string{model.BuiltinToken(0, model.LaneVLESS)})
	premium, _ := st.CreateGroup("Premium", []string{model.BuiltinToken(0, model.LaneHysteria)})

	plan := &model.TariffPlan{Slug: "p", Name: "P", PriceRub: 100, PeriodDays: 30, Enabled: true, GroupIDs: []int64{basic.ID}}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := st.ApplyUserPlan(planWrite(u.ID, plan)); err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	plan.GroupIDs = []int64{premium.ID}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("re-save plan: %v", err)
	}
	all, via := groupNamesOf(t, st, u.ID)
	if !eq(all, []string{"Premium"}) || !eq(via, []string{"Premium"}) {
		t.Fatalf("after editing the plan: groups=%v viaPlan=%v, want [Premium] both", all, via)
	}

	// And the plan reads back with what it grants, for the editor and the API.
	got, err := st.GetTariffPlan(plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if len(got.GroupIDs) != 1 || got.GroupIDs[0] != premium.ID {
		t.Fatalf("plan.GroupIDs = %v, want [%d]", got.GroupIDs, premium.ID)
	}

	// Now the collision case, which is what exercises the upsert in syncPlanMembersOn:
	// a user on the plan is already in the group the edit adds, by hand. The row must
	// stay MANUAL — re-stamping it as the plan's would hand it to the next plan switch
	// to undo.
	other, _ := st.CreateUser("second", "uuid2", "pw", "tok2", 0, 0, 0)
	if err := st.ApplyUserPlan(planWrite(other.ID, plan)); err != nil { // plan grants Premium
		t.Fatalf("apply plan: %v", err)
	}
	if err := st.SetUserGroups(other.ID, []int64{basic.ID, premium.ID}); err != nil {
		t.Fatalf("set groups: %v", err) // Basic by hand, Premium still the plan's
	}
	plan.GroupIDs = []int64{basic.ID} // the plan now grants what they already have
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("re-save plan: %v", err)
	}
	all, via = groupNamesOf(t, st, other.ID)
	if !eq(all, []string{"Basic"}) {
		t.Fatalf("groups=%v, want [Basic]: the plan's own Premium goes, the hand-added Basic stays", all)
	}
	if len(via) != 0 {
		t.Fatalf("the upsert re-stamped a hand-assigned row as the plan's: viaPlan=%v", via)
	}
}

// The purchase path grants the groups inside the order's paid claim: if the plan can't
// land, neither may the claim — the same money-safety rule the rest of the write obeys.
func TestConfirmPaymentAppliesPlanGroups(t *testing.T) {
	st, u, plan, order, w := planWriteFixture(t)
	defer st.Close()

	premium, err := st.CreateGroup("Premium", []string{model.BuiltinToken(0, model.LaneHysteria)})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	plan.GroupIDs = []int64{premium.ID}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	w.GroupIDs = plan.GroupIDs

	claimed, err := st.ConfirmPaymentOrder(order.ID, time.Now().Unix(), w)
	if err != nil || !claimed {
		t.Fatalf("confirm: claimed=%v err=%v", claimed, err)
	}
	all, via := groupNamesOf(t, st, u.ID)
	if !eq(all, []string{"Premium"}) || !eq(via, []string{"Premium"}) {
		t.Fatalf("paying for the plan did not grant its group: groups=%v viaPlan=%v", all, via)
	}

	// A rolled-back confirm leaves no membership behind either.
	other, _ := st.CreateUser("second", "uuid2", "pw", "tok2", 0, 0, 0)
	order2, _ := st.CreatePaymentOrder(other.ID, plan.ID, plan.PriceRub)
	w2 := w
	w2.UserID = other.ID
	restore := failUserWrites(t, st)
	if _, err := st.ConfirmPaymentOrder(order2.ID, time.Now().Unix(), w2); err == nil {
		t.Fatal("expected the confirm to fail")
	}
	restore()
	if all, _ = groupNamesOf(t, st, other.ID); len(all) != 0 {
		t.Fatalf("a rolled-back confirm left the plan's groups behind: %v", all)
	}
}
