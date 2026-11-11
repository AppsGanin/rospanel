package store

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// device_limit 0 takes the panel's address default; -1 is no limit whatever it is.
// The cut, the status and the counted-active number all go by the same rule.
func TestIPFallbackLimitAppliesToUsersOnZero(t *testing.T) {
	t.Parallel()
	st := dcStore(t)
	// Run in the recent past, so the status read at the real time sees the stamp aged
	// past the grace and the last sightings still inside the window.
	start := time.Now().Unix() - 330
	mk := func(name string, limit int) model.User {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, limit)
		if err != nil {
			t.Fatal(err)
		}
		return *u
	}
	onDefault, none := mk("default", 0), mk("none", model.IPLimitUnlimited)
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	set.IPFallbackLimit = 1
	if err := st.SetHWIDSettings(set); err != nil {
		t.Fatal(err)
	}
	two := func(at int64) []string { return []string{"10.0.0.9", "203.0.113.7"} }
	// The unlimited one first: its passes would otherwise age out and clear the
	// other's over-the-limit stamp.
	if cut := runDevices(t, st, none.ID, start, 300, two); cut >= 0 {
		t.Errorf("a user with no IP limit was cut %ds in", cut)
	}
	if cut := runDevices(t, st, onDefault.ID, start, 300, two); cut < 0 {
		t.Error("a user on the default of 1 with two addresses was never cut")
	}
	// Both still in use right now, for the status and the count.
	recent := time.Now().Unix() - 5
	var hits []ConnectionHit
	for _, id := range []int64{onDefault.ID, none.ID} {
		for _, ip := range two(0) {
			hits = append(hits, ConnectionHit{UserID: id, IP: ip, SeenAt: recent, Hits: 1})
		}
	}
	if err := st.AddConnections(hits); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetUser(onDefault.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IPLimit != 1 || got.Status != model.StatusDeviceLimited {
		t.Errorf("on the default: ip_limit %d status %s, want 1 and device_limited", got.IPLimit, got.Status)
	}
	if got, _ := st.GetUser(none.ID); got.IPLimit != 0 || got.Status != model.StatusActive {
		t.Errorf("no limit: ip_limit %d status %s, want 0 and active", got.IPLimit, got.Status)
	}
	c, err := st.CountUsers(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if c.Active != 1 {
		t.Errorf("%d active users counted, want 1 (the one with no limit)", c.Active)
	}
}

func TestEffectiveIPLimit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ own, def, want int }{
		{0, 0, 0}, {0, 3, 3}, {2, 3, 2}, {model.IPLimitUnlimited, 3, 0},
	} {
		if got := model.EffectiveIPLimit(c.own, c.def); got != c.want {
			t.Errorf("EffectiveIPLimit(%d, %d) = %d, want %d", c.own, c.def, got, c.want)
		}
	}
}
