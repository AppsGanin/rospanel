package store

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func openGroupStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "groups.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// The resolver is the security core: no membership ⇒ unrestricted; any membership ⇒
// restricted to the union of grants. A wrong answer here is a user reaching a lane
// they shouldn't (or losing one they should).
func TestAccessResolution(t *testing.T) {
	t.Parallel()
	st := openGroupStore(t)
	u1, _ := st.CreateUser("free", "uuid1", "pw", "tok1", 0, 0, 0)
	u2, _ := st.CreateUser("vip", "uuid2", "pw", "tok2", 0, 0, 0)

	ga, err := st.CreateGroup("A", []string{model.BuiltinToken(0, model.LaneVLESS), model.InboundToken(5)}, 0)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	gb, _ := st.CreateGroup("B", []string{model.BuiltinToken(0, model.LaneReality)}, 0)
	if err := st.SetUserGroups(u2.ID, []int64{ga.ID, gb.ID}); err != nil {
		t.Fatalf("set groups: %v", err)
	}

	// u1: no group ⇒ everything.
	a1, _ := st.UserAccess(u1.ID)
	if !a1.All || !a1.AllowsBuiltin(0, model.LaneHysteria) || !a1.AllowsInbound(999) {
		t.Errorf("ungrouped user should be unrestricted: %+v", a1)
	}

	// u2: union of A+B ⇒ VLESS + REALITY + inbound 5, but NOT Hysteria, NOT inbound 6.
	a2, _ := st.UserAccess(u2.ID)
	if a2.All {
		t.Fatal("grouped user must be restricted")
	}
	if !a2.AllowsBuiltin(0, model.LaneVLESS) || !a2.AllowsBuiltin(0, model.LaneReality) || !a2.AllowsInbound(5) {
		t.Errorf("granted items missing: %+v", a2)
	}
	if a2.AllowsBuiltin(0, model.LaneHysteria) || a2.AllowsInbound(6) || a2.AllowsBuiltin(7, model.LaneVLESS) {
		t.Errorf("un-granted items allowed: %+v", a2)
	}

	// The batch map must agree with the single-user resolver.
	m, _ := st.AccessMap()
	if _, restricted := m[u2.ID]; !restricted {
		t.Error("access map should list the restricted user")
	}
	if _, present := m[u1.ID]; present {
		t.Error("access map must NOT list an unrestricted user (default = allow)")
	}
	if !model.AccessOf(m, u1.ID).All {
		t.Error("a user absent from the map resolves to unrestricted")
	}
}

// A group saved with no connection ticked is a tier for its speed cap, not for
// access: its members keep every connection. That is decided when it is saved, not
// read off the grant list — so a group whose grants were swept away with the
// inbound they named keeps its members restricted, reaching nothing, instead of
// suddenly handing them everything. And an empty group from before the flag existed
// (every existing row got 1) keeps meaning what it always meant.
func TestGroupsSavedWithoutGrantsDoNotLimitAccess(t *testing.T) {
	t.Parallel()
	st := openGroupStore(t)
	mk := func(name string) int64 {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	speedOnly, _ := st.CreateGroup("speed tier", nil, 5000)
	vless, _ := st.CreateGroup("vless", []string{model.BuiltinToken(0, model.LaneVLESS)}, 0)
	premium, _ := st.CreateGroup("premium", []string{model.InboundToken(7)}, 0)
	legacy, _ := st.CreateGroup("legacy locked", nil, 0)
	if _, err := st.db.Exec(`UPDATE groups SET limits_access = 1 WHERE id = ?`, legacy.ID); err != nil {
		t.Fatal(err) // what migration 0078 gives a group that existed empty before it
	}

	tier, both, swept, locked := mk("tier"), mk("both"), mk("swept"), mk("locked")
	for uid, groups := range map[int64][]int64{
		tier: {speedOnly.ID}, both: {speedOnly.ID, vless.ID}, swept: {premium.ID}, locked: {legacy.ID},
	} {
		if err := st.SetUserGroups(uid, groups); err != nil {
			t.Fatal(err)
		}
	}
	// premium's only inbound is deleted, and its grant with it.
	if err := st.DeleteInboundGrants(7); err != nil {
		t.Fatal(err)
	}

	m, err := st.AccessMap()
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, uid int64, wantAll bool, reachesVLESS bool) {
		t.Helper()
		one, err := st.UserAccess(uid)
		if err != nil {
			t.Fatal(err)
		}
		for src, a := range map[string]model.Access{"UserAccess": one, "AccessMap": model.AccessOf(m, uid)} {
			if a.All != wantAll || a.AllowsBuiltin(0, model.LaneVLESS) != reachesVLESS {
				t.Errorf("%s %s: all=%v vless=%v — want all=%v vless=%v", src, name, a.All, a.AllowsBuiltin(0, model.LaneVLESS), wantAll, reachesVLESS)
			}
		}
	}
	check("speed tier only", tier, true, true)
	check("speed tier + vless group", both, false, true)
	check("grants swept away", swept, false, false)
	check("legacy empty group", locked, false, false)

	if g, _ := st.GetGroup(speedOnly.ID); g.LimitsAccess {
		t.Error("a group created with no grants reads as limiting access")
	}
	if g, _ := st.GetGroup(premium.ID); !g.LimitsAccess {
		t.Error("a sweep changed whether a group limits access")
	}
	// Saving the swept group with nothing ticked is the operator's decision to open it.
	if err := st.UpdateGroup(premium.ID, "premium", nil, 0); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.UserAccess(swept); !a.All {
		t.Error("a group re-saved with no grants still restricts its members")
	}
	if refs, _ := st.GroupsForUser(both); len(refs) != 2 || refs[0].LimitsAccess == refs[1].LimitsAccess {
		t.Errorf("group refs do not say which group limits access: %+v", refs)
	}
}

// FK cascades are load-bearing for cleanup: deleting a group must not strand its
// membership or grants, and deleting a user must not strand their membership.
func TestGroupCascades(t *testing.T) {
	t.Parallel()
	st := openGroupStore(t)
	u, _ := st.CreateUser("x", "uuid", "pw", "tok", 0, 0, 0)
	g, _ := st.CreateGroup("g", []string{model.BuiltinToken(0, model.LaneVLESS)}, 0)
	_ = st.SetUserGroups(u.ID, []int64{g.ID})

	count := func(table string) int {
		var n int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	if count("group_members") != 1 || count("group_grants") != 1 {
		t.Fatalf("setup: members=%d grants=%d", count("group_members"), count("group_grants"))
	}

	if err := st.DeleteGroup(g.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if count("group_members") != 0 || count("group_grants") != 0 {
		t.Errorf("deleting a group left orphans: members=%d grants=%d (foreign_keys off?)",
			count("group_members"), count("group_grants"))
	}

	// And deleting a user cascades their membership.
	g2, _ := st.CreateGroup("g2", nil, 0)
	_ = st.SetGroupMembers(g2.ID, []int64{u.ID})
	if count("group_members") != 1 {
		t.Fatalf("member not set")
	}
	if err := st.DeleteUser(u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if count("group_members") != 0 {
		t.Error("deleting a user left a membership orphan (foreign_keys off?)")
	}
}

// Group names are unique case-insensitively, so a chip can't be ambiguous.
func TestGroupNameUniqueCI(t *testing.T) {
	t.Parallel()
	st := openGroupStore(t)
	if _, err := st.CreateGroup("VIP", nil, 0); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.CreateGroup("vip", nil, 0); err == nil {
		t.Error("expected a case-insensitive name conflict")
	}
}
