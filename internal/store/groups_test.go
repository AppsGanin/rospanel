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
	st := openGroupStore(t)
	u1, _ := st.CreateUser("free", "uuid1", "pw", "tok1", 0, 0, 0)
	u2, _ := st.CreateUser("vip", "uuid2", "pw", "tok2", 0, 0, 0)

	ga, err := st.CreateGroup("A", []string{model.BuiltinToken(0, model.LaneVLESS), model.InboundToken(5)})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	gb, _ := st.CreateGroup("B", []string{model.BuiltinToken(0, model.LaneReality)})
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

// A member in a group granting nothing reaches nothing — the deliberate "revoke via
// empty group" semantics, not an accidental unrestricted fallback.
func TestEmptyGroupGrantsNothing(t *testing.T) {
	st := openGroupStore(t)
	u, _ := st.CreateUser("x", "uuid", "pw", "tok", 0, 0, 0)
	g, _ := st.CreateGroup("locked", nil)
	_ = st.SetUserGroups(u.ID, []int64{g.ID})

	a, _ := st.UserAccess(u.ID)
	if a.All {
		t.Fatal("a user in an empty group must be restricted, not unrestricted")
	}
	if a.AllowsBuiltin(0, model.LaneVLESS) || a.AllowsInbound(1) {
		t.Error("an empty group must grant nothing")
	}
}

// FK cascades are load-bearing for cleanup: deleting a group must not strand its
// membership or grants, and deleting a user must not strand their membership.
func TestGroupCascades(t *testing.T) {
	st := openGroupStore(t)
	u, _ := st.CreateUser("x", "uuid", "pw", "tok", 0, 0, 0)
	g, _ := st.CreateGroup("g", []string{model.BuiltinToken(0, model.LaneVLESS)})
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
	g2, _ := st.CreateGroup("g2", nil)
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
	st := openGroupStore(t)
	if _, err := st.CreateGroup("VIP", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.CreateGroup("vip", nil); err == nil {
		t.Error("expected a case-insensitive name conflict")
	}
}
