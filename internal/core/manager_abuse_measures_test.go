package core

import (
	"context"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// measuresManager is abuseTestManager with a ladder switched on: warn at 2, throttle
// at 4 (to 512 kbit/s), switch off at 6, for one hour.
func measuresManager(t *testing.T) (*Manager, int64) {
	t.Helper()
	m, uid := abuseTestManager(t)
	if err := m.SetAbuseMeasures(model.AbuseMeasures{WarnMin: 2, ThrottleMin: 4, ThrottleKbps: 512, DisableMin: 6, Hours: 1}); err != nil {
		t.Fatalf("set measures: %v", err)
	}
	return m, uid
}

// hit records n matches for the user and flushes them, the way the poll would.
func hit(m *Manager, uid int64, n int) {
	for range n {
		m.recordAbuse(uid, "203.0.113.5")
	}
	m.FlushAbuse()
}

func userOf(t *testing.T, m *Manager, uid int64) model.User {
	t.Helper()
	u, err := m.store.GetUser(uid)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	return *u
}

func lastEvents(t *testing.T, m *Manager, uid int64) []string {
	t.Helper()
	evs, err := m.store.ListUserEvents(uid, 50, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Action)
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func count(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

func TestAbuseMeasuresClimbTheLadderOnce(t *testing.T) {
	m, uid := measuresManager(t)
	if err := m.store.SetUserSpeedLimit(uid, 4096); err != nil {
		t.Fatal(err)
	}

	// Two matches: a warning, and only a warning — recorded once for the day
	// however many flushes carry it.
	hit(m, uid, 2)
	hit(m, uid, 1)
	u := userOf(t, m, uid)
	if u.AbuseAction != "" || u.SpeedLimit != 4096 || !u.Enabled {
		t.Fatalf("after 3 matches the user must only be warned: %+v", u)
	}
	if n := count(lastEvents(t, m, uid), model.EventAbuseWarned); n != 1 {
		t.Fatalf("warned %d times, want exactly once a day", n)
	}

	// Four: throttled, the operator's own cap remembered.
	hit(m, uid, 1)
	u = userOf(t, m, uid)
	if u.AbuseAction != model.AbuseActionThrottle || u.SpeedLimit != 512 || u.AbusePrevSpeed != 4096 {
		t.Fatalf("after 4 matches the user must be throttled to 512 remembering 4096: %+v", u)
	}
	if u.AbuseUntil < time.Now().Add(50*time.Minute).Unix() {
		t.Fatalf("throttle deadline not an hour away: %d", u.AbuseUntil)
	}
	hit(m, uid, 1) // five: nothing new
	if got := count(lastEvents(t, m, uid), model.EventAbuseThrottled); got != 1 {
		t.Fatalf("throttled %d times, want once", got)
	}

	// Six: switched off. The throttle is undone on the way, so the lift has one
	// thing to restore.
	hit(m, uid, 1)
	u = userOf(t, m, uid)
	if u.AbuseAction != model.AbuseActionDisable || u.Enabled || u.SpeedLimit != 4096 {
		t.Fatalf("after 6 matches the user must be off with their own speed back: %+v", u)
	}
	hit(m, uid, 3) // more of the same changes nothing
	evs := lastEvents(t, m, uid)
	if count(evs, model.EventAbuseDisabled) != 1 {
		t.Fatalf("switched off %d times, want once", count(evs, model.EventAbuseDisabled))
	}

	// The hour passes: back on, measure forgotten, journal says why.
	m.LiftAbuseMeasures(u.AbuseUntil + 1)
	u = userOf(t, m, uid)
	if !u.Enabled || u.AbuseAction != "" || u.AbuseUntil != 0 || u.SpeedLimit != 4096 {
		t.Fatalf("after the lift the user must be as they were: %+v", u)
	}
	if !has(lastEvents(t, m, uid), model.EventAbuseLifted) {
		t.Fatal("no lifted event")
	}
	// Not due yet: nothing happens.
	hit(m, uid, 6)
	u = userOf(t, m, uid)
	m.LiftAbuseMeasures(u.AbuseUntil - 1)
	if userOf(t, m, uid).Enabled {
		t.Fatal("lifted before the deadline")
	}
}

func TestAbuseThrottleLiftRestoresTheOldCap(t *testing.T) {
	m, uid := measuresManager(t)
	hit(m, uid, 4)
	u := userOf(t, m, uid)
	if u.AbuseAction != model.AbuseActionThrottle || u.SpeedLimit != 512 || u.AbusePrevSpeed != 0 {
		t.Fatalf("not throttled from unlimited: %+v", u)
	}
	m.LiftAbuseMeasures(u.AbuseUntil)
	u = userOf(t, m, uid)
	if u.SpeedLimit != 0 || u.AbuseAction != "" {
		t.Fatalf("lift must give the unlimited speed back: %+v", u)
	}
}

// An operator who switches the user back on, or sets a speed by hand, has decided;
// the panel forgets the measure instead of lifting it later into a state nobody
// chose.
func TestOperatorOverrulesTheMeasure(t *testing.T) {
	m, uid := measuresManager(t)
	ctx := context.Background()

	hit(m, uid, 6)
	if userOf(t, m, uid).Enabled {
		t.Fatal("not switched off")
	}
	if err := m.SetUserEnabled(ctx, uid, true); err != nil {
		t.Fatal(err)
	}
	u := userOf(t, m, uid)
	if u.AbuseAction != "" || u.AbuseUntil != 0 {
		t.Fatalf("enabling by hand must forget the measure: %+v", u)
	}
	// The forgotten measure must not resurface: nothing is due, and a lift at any
	// time changes nothing.
	m.LiftAbuseMeasures(time.Now().Add(48 * time.Hour).Unix())
	if u = userOf(t, m, uid); !u.Enabled {
		t.Fatal("a forgotten measure was lifted anyway")
	}

	// Same for a speed set by hand over a throttle.
	m2, uid2 := measuresManager(t)
	hit(m2, uid2, 4)
	if err := m2.SetUserSpeedLimit(ctx, uid2, 2048); err != nil {
		t.Fatal(err)
	}
	u = userOf(t, m2, uid2)
	if u.AbuseAction != "" || u.SpeedLimit != 2048 {
		t.Fatalf("a speed set by hand must stand and forget the throttle: %+v", u)
	}
	m2.LiftAbuseMeasures(time.Now().Add(48 * time.Hour).Unix())
	if userOf(t, m2, uid2).SpeedLimit != 2048 {
		t.Fatal("the lift overwrote the operator's speed")
	}
}

// A user the operator switched off is not the panel's to switch back on: the
// disable rung records nothing for them.
func TestAbuseDisableSkipsAnOperatorDisabledUser(t *testing.T) {
	m, uid := measuresManager(t)
	if err := m.store.SetUserEnabled(uid, false); err != nil {
		t.Fatal(err)
	}
	hit(m, uid, 6)
	u := userOf(t, m, uid)
	if u.AbuseAction != "" {
		t.Fatalf("the panel claimed a switch-off it did not do: %+v", u)
	}
}

func TestAbuseMeasuresOffDoNothing(t *testing.T) {
	m, uid := abuseTestManager(t) // ladder all zeros
	hit(m, uid, 50)
	u := userOf(t, m, uid)
	if u.AbuseAction != "" || !u.Enabled || u.SpeedLimit != 0 || u.AbuseWarnedDay != "" {
		t.Fatalf("measures off must leave the user alone: %+v", u)
	}
}

func TestAbuseMeasuresValidate(t *testing.T) {
	bad := []model.AbuseMeasures{
		{ThrottleMin: 3, ThrottleKbps: 0, Hours: 1},  // a throttle to nothing
		{DisableMin: 3, Hours: 0},                     // a switch-off with no end
		{DisableMin: 3, Hours: 24*30 + 1},             // or one past the ceiling
		{WarnMin: -1},                                 // a negative threshold
	}
	for i, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("bad ladder %d accepted: %+v", i, b)
		}
	}
	good := []model.AbuseMeasures{
		{},                            // everything off
		{WarnMin: 5},                  // a warning needs no hours
		{ThrottleMin: 5, ThrottleKbps: 256, Hours: 1},
		{DisableMin: 5, Hours: 24 * 30},
	}
	for i, g := range good {
		if err := g.Validate(); err != nil {
			t.Errorf("good ladder %d refused: %v", i, err)
		}
	}
}
