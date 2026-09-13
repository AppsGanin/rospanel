package store

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The speed cap in force, as the shaper on the master and every node receive it.
// A group that sets a cap takes priority over the user's own — their tariff's or
// their card's — a member of several capped groups gets the highest, and a
// blocklist throttle is never loosened by a group.
func TestTheSpeedCapInForce(t *testing.T) {
	st := newStore(t)
	mk := func(name string, kbps int) int64 {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if kbps > 0 {
			if err := st.SetUserSpeedLimit(u.ID, kbps); err != nil {
				t.Fatal(err)
			}
		}
		return u.ID
	}
	group := func(name string, kbps int) int64 {
		t.Helper()
		g, err := st.CreateGroup(name, nil, kbps)
		if err != nil {
			t.Fatal(err)
		}
		return g.ID
	}
	fast, slow, none := group("fast", 8000), group("slow", 2000), group("uncapped", 0)

	own := mk("own", 1000)             // no group: their own cap
	overridden := mk("override", 1000) // a capped group wins over their own
	both := mk("both", 0)              // two capped groups: the highest
	uncapped := mk("uncapped", 1000)   // a group that sets nothing leaves their own
	throttled := mk("throttled", 512)  // throttled below the group: the throttle holds
	lenient := mk("lenient", 3000)     // "throttled" above the group: the group is stricter
	off := mk("off", 0)                // capped by a group but switched off: not shaped

	for uid, groups := range map[int64][]int64{
		overridden: {fast}, both: {slow, fast}, uncapped: {none},
		throttled: {fast}, lenient: {slow}, off: {fast},
	} {
		if err := st.SetUserGroups(uid, groups); err != nil {
			t.Fatal(err)
		}
	}
	until := time.Now().Add(time.Hour).Unix()
	for _, uid := range []int64{throttled, lenient} {
		if err := st.SetAbuseMeasure(uid, model.AbuseActionThrottle, until, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetUserEnabled(off, false); err != nil {
		t.Fatal(err)
	}

	want := map[int64]int{own: 1000, overridden: 8000, both: 8000, uncapped: 1000, throttled: 512, lenient: 2000}
	capped, err := st.CappedUsers(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	shaped, err := st.ShapedUsers(time.Now().Add(-time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	names := map[int64]string{own: "own", overridden: "override", both: "both", uncapped: "uncapped", throttled: "throttled", lenient: "lenient", off: "off"}
	for uid, name := range names {
		w, ok := want[uid]
		if got, in := capped[uid]; in != ok || got != w {
			t.Errorf("CappedUsers %s: %d (present %v), want %d (present %v)", name, got, in, w, ok)
		}
		if got, in := shaped[uid]; in != ok || got.Kbps != w {
			t.Errorf("ShapedUsers %s: %d (present %v), want %d (present %v)", name, got.Kbps, in, w, ok)
		}
	}

	if g, _ := st.GroupSpeedLimit(both); g != 8000 {
		t.Errorf("GroupSpeedLimit(both) = %d, want 8000", g)
	}
	if g, _ := st.GroupSpeedLimit(own); g != 0 {
		t.Errorf("GroupSpeedLimit(own) = %d, want 0", g)
	}
	refs, _ := st.GroupsForUser(both)
	if len(refs) != 2 || refs[0].SpeedLimit+refs[1].SpeedLimit != 10000 {
		t.Errorf("the user's group refs do not carry the caps: %+v", refs)
	}
	if all, _ := st.GroupsForAllUsers(); len(all[both]) != 2 || all[both][0].SpeedLimit+all[both][1].SpeedLimit != 10000 {
		t.Errorf("the list's group refs do not carry the caps: %+v", all[both])
	}
	if g, _ := st.GetGroup(fast); g == nil || g.SpeedLimit != 8000 {
		t.Errorf("GetGroup: %+v", g)
	}
	if err := st.UpdateGroup(fast, "fast", nil, 4000); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.Groups(); !hasGroupSpeed(list, fast, 4000) {
		t.Errorf("Groups after an update: %+v", list)
	}
}

func hasGroupSpeed(list []model.Group, id int64, kbps int) bool {
	for _, g := range list {
		if g.ID == id {
			return g.SpeedLimit == kbps
		}
	}
	return false
}

// A throttle holds against everything that writes a cap while it is in force. A
// tariff edit lands in the cap the lift will put back, not over the throttle; and a
// cap that has become stricter than the throttle — by that edit, or by the user
// leaving the group that outranked it — is the one in force meanwhile.
func TestAThrottleHoldsAgainstTariffAndGroupChanges(t *testing.T) {
	st := newStore(t)
	plan := &model.TariffPlan{Slug: "speedy", Name: "Speedy", PriceRub: 100, PeriodDays: 30, SpeedLimit: 10000, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser("abuser", "uuid-abuser", "pw", "tok-abuser", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyUserPlan(UserPlanWrite{UserID: u.ID, PlanID: plan.ID, SpeedLimit: 10000, ResetPeriod: "none"}); err != nil {
		t.Fatal(err)
	}
	// The ladder throttles them to 512, remembering the tariff's 10000.
	if err := st.SetUserSpeedLimit(u.ID, 512); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAbuseMeasure(u.ID, model.AbuseActionThrottle, time.Now().Add(time.Hour).Unix(), 10000); err != nil {
		t.Fatal(err)
	}
	inForce := func() int {
		t.Helper()
		capped, err := st.CappedUsers(time.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
		return capped[u.ID]
	}

	// The tariff is edited to 20000: the throttle stays, the lift will give 20000.
	if _, err := st.SetPlanUsersSpeedLimit(plan.ID, 20000); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetUser(u.ID)
	if got.SpeedLimit != 512 || got.AbusePrevSpeed != 20000 || inForce() != 512 {
		t.Errorf("tariff edit during a throttle: speed %d prev %d in force %d — want 512, 20000, 512", got.SpeedLimit, got.AbusePrevSpeed, inForce())
	}
	// Re-applying the plan (a renewal) behaves the same way.
	if err := st.ApplyUserPlan(UserPlanWrite{UserID: u.ID, PlanID: plan.ID, SpeedLimit: 256, ResetPeriod: "none"}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetUser(u.ID)
	if got.SpeedLimit != 512 || got.AbusePrevSpeed != 256 {
		t.Errorf("plan applied during a throttle: speed %d prev %d — want 512 and 256", got.SpeedLimit, got.AbusePrevSpeed)
	}
	// 256 is now stricter than the throttle, and it is what applies.
	if inForce() != 256 {
		t.Errorf("a tariff cap stricter than the throttle: %d in force, want 256", inForce())
	}
}
