package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A group's speed cap from the manager's side: what an edit keeps and changes, what
// the nodes are handed, and the blocklist ladder reading the cap actually in force.

func TestGroupSpeedLimitEdits(t *testing.T) {
	m := bulkTestManager(t)
	if _, err := m.CreateGroup("neg", nil, -1); err == nil {
		t.Error("a negative group cap was accepted")
	}
	g, err := m.CreateGroup("slow", nil, 2000)
	if err != nil {
		t.Fatal(err)
	}
	uid := mkUser(t, m, "member", 0)
	if err := m.SetUserGroups(uid, []int64{g.ID}); err != nil {
		t.Fatal(err)
	}
	if got := m.SpeedLimits()[model.UserEmail(uid)]; got != 2000 {
		t.Fatalf("nodes are handed %d for a member of a 2000 kbit/s group, want 2000", got)
	}

	// An edit that says nothing about the speed keeps it.
	if err := m.UpdateGroup(g.ID, "slower", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.GetGroup(g.ID); got.SpeedLimit != 2000 || got.Name != "slower" {
		t.Errorf("rename kept the cap? %+v", got)
	}
	zero := 0
	if err := m.UpdateGroup(g.ID, "slower", nil, &zero); err != nil {
		t.Fatal(err)
	}
	if _, capped := m.SpeedLimits()[model.UserEmail(uid)]; capped {
		t.Error("clearing the group's cap left the member capped")
	}
	neg := -5
	if err := m.UpdateGroup(g.ID, "slower", nil, &neg); err == nil {
		t.Error("a negative cap was accepted on edit")
	}
	if err := m.UpdateGroup(g.ID+100, "ghost", nil, nil); err == nil {
		t.Error("editing a group that does not exist succeeded")
	}
}

func TestSameTokens(t *testing.T) {
	if !sameTokens([]string{"a", "b"}, []string{"b", "a"}) {
		t.Error("the same tokens in another order read as a change")
	}
	for _, c := range [][2][]string{{{"a"}, {"a", "b"}}, {{"a", "b"}, {"a", "c"}}, {nil, {"a"}}} {
		if sameTokens(c[0], c[1]) {
			t.Errorf("%v and %v read as the same", c[0], c[1])
		}
	}
}

// The throttle's "already slower" check reads the cap in force: a member of a group
// capped below the throttle is not throttled — which would only journal and notify a
// slowdown that changes nothing.
func TestThrottleReadsTheGroupCap(t *testing.T) {
	m, uid := measuresManager(t) // throttle to 512 kbit/s at 4 matches
	g, err := m.CreateGroup("crawl", nil, 256)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserGroups(uid, []int64{g.ID}); err != nil {
		t.Fatal(err)
	}
	hit(m, uid, 4)
	if u := userOf(t, m, uid); u.AbuseAction != "" || u.SpeedLimit != 0 {
		t.Errorf("a member capped at 256 kbit/s was throttled to 512: %+v", u)
	}

	// Above the throttle, the throttle applies — and the nodes get the throttle.
	m2, uid2 := measuresManager(t)
	g2, _ := m2.CreateGroup("fast", nil, 50000)
	if err := m2.SetUserGroups(uid2, []int64{g2.ID}); err != nil {
		t.Fatal(err)
	}
	hit(m2, uid2, 4)
	if u := userOf(t, m2, uid2); u.AbuseAction != model.AbuseActionThrottle {
		t.Fatalf("a member of a fast group was not throttled: %+v", u)
	}
	if got := m2.SpeedLimits()[model.UserEmail(uid2)]; got != 512 {
		t.Errorf("a throttled member of a 50 Mbit/s group is shaped at %d, want the throttle's 512", got)
	}
}

// Only a change of who reaches which lane restarts the tunnels. The group editor
// saves the name, the speed cap and the member list together, so each of those must
// leave the reconcile alone when it did not actually change access.
func TestGroupEditsReconcileOnlyForAccess(t *testing.T) {
	m := bulkTestManager(t)
	m.reconcileCh = make(chan struct{}, 1)
	g, err := m.CreateGroup("g", []string{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, b := mkUser(t, m, "a", 0), mkUser(t, m, "b", 0)
	if err := m.SetGroupMembers(g.ID, []int64{a, b}); err != nil {
		t.Fatal(err)
	}
	m.structuralPending.Store(false)

	kbps := 4000
	if err := m.UpdateGroup(g.ID, "renamed", []string{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS)}, &kbps); err != nil {
		t.Fatal(err)
	}
	if err := m.SetGroupMembers(g.ID, []int64{b, a, a}); err != nil {
		t.Fatal(err)
	}
	if m.structuralPending.Load() {
		t.Fatal("a rename, a speed cap and the same members restarted the tunnels")
	}
	if got := m.SpeedLimits()[model.UserEmail(a)]; got != 4000 {
		t.Errorf("the new cap is not in force: %d", got)
	}

	if err := m.SetGroupMembers(g.ID, []int64{a}); err != nil {
		t.Fatal(err)
	}
	if !m.structuralPending.Load() {
		t.Error("removing a member did not reconcile")
	}
	m.structuralPending.Store(false)
	if err := m.UpdateGroup(g.ID, "renamed", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !m.structuralPending.Load() {
		t.Error("taking a grant away did not reconcile")
	}
}
