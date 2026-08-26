package store

import (
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// see runs the user's devices for `span` seconds from `start`, refreshing each listed
// address every 10s exactly as the access tap would, and reports the first second at
// which the user was cut out of the config (-1 for never).
func runDevices(t *testing.T, st *Store, id int64, start, span int64, live func(at int64) []string) int64 {
	t.Helper()
	for at := start; at <= start+span; at += 10 {
		var hits []ConnectionHit
		for _, ip := range live(at) {
			hits = append(hits, ConnectionHit{UserID: id, IP: ip, SeenAt: at, Hits: 1})
		}
		if len(hits) > 0 {
			if err := st.AddConnections(hits); err != nil {
				t.Fatalf("connections at %d: %v", at, err)
			}
		}
		if err := st.StampDeviceOverLimit(at); err != nil {
			t.Fatalf("stamp at %d: %v", at, err)
		}
		if !dcWorking(t, st, id, at) {
			return at - start
		}
	}
	return -1
}

// The report behind issue #66, and the commoner version of it nobody reports: a mobile
// carrier rotating the public address inside its own pool. Both leave one address behind
// that keeps a fresh sighting until it ages out, and neither may cost the user their
// connection.
func TestAbandonedAddressNeverCutsTheUser(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()
	u := dcUser(t, st, "phone") // device_limit 1

	// Runs on 10.0.0.1 for a minute, then everything moves to 192.168.1.5 for five.
	cut := runDevices(t, st, u.ID, now, 360, func(at int64) []string {
		if at < now+60 {
			return []string{"10.0.0.1"}
		}
		return []string{"192.168.1.5"}
	})
	if cut >= 0 {
		t.Errorf("a device that changed address was cut %ds in — the whole point of "+
			"DeviceLimitGrace is that the abandoned address leaves the window first", cut)
	}
}

// The paired case that makes the grace a trade and not a giveaway: two addresses that
// BOTH keep being used are still cut, just later.
func TestSustainedSharingIsStillCut(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()
	u := dcUser(t, st, "shared")

	cut := runDevices(t, st, u.ID, now, 600, func(int64) []string {
		return []string{"10.0.0.9", "203.0.113.7"}
	})
	if cut < 0 {
		t.Fatal("two addresses in continuous simultaneous use were never cut")
	}
	if cut < model.DeviceLimitGrace || cut > model.DeviceLimitGrace+30 {
		t.Errorf("cut %ds in, want about %ds — the grace must delay the cut, not "+
			"cancel or shorten it", cut, model.DeviceLimitGrace)
	}
}

// An out-of-date stamp must never be what holds someone out: the enforcement query
// checks the live count as well, so a user back under their limit is admitted even if
// nothing has run to clear the stamp.
func TestStaleStampCannotHoldSomeoneOut(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()
	u := dcUser(t, st, "phone",
		ConnectionHit{IP: "10.0.0.1", SeenAt: now},
		ConnectionHit{IP: "192.168.1.5", SeenAt: now},
	)
	if err := st.StampDeviceOverLimit(now); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	// Long past the grace, with the stamp never cleared, but both sightings have aged
	// out of the window.
	at := now + model.DeviceLimitGrace + model.DeviceOnlineWindow
	if !dcWorking(t, st, u.ID, at) {
		t.Error("a user under their limit stayed cut because of a stamp nobody cleared")
	}
}

// Switching the address counter off must not leave stamps behind that fire the moment
// an operator switches it back on.
func TestHWIDModeClearsStamps(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()
	u := dcUser(t, st, "shared",
		ConnectionHit{IP: "10.0.0.9", SeenAt: now},
		ConnectionHit{IP: "203.0.113.7", SeenAt: now},
	)
	if err := st.StampDeviceOverLimit(now); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if err := st.SetDeviceCountMode(model.DeviceCountHWID); err != nil {
		t.Fatalf("hwid: %v", err)
	}
	if err := st.StampDeviceOverLimit(now + 10); err != nil {
		t.Fatalf("stamp hwid: %v", err)
	}
	got, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DeviceOverSince != 0 {
		t.Errorf("stamp survived the switch to hwid mode (%d) — switching back would "+
			"cut this user instantly", got.DeviceOverSince)
	}
}

// The status the panel, the API and the bot quote must follow the cut, not precede it.
func TestOverLimitWithinGraceIsNotShownAsLimited(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()
	u := dcUser(t, st, "shared",
		ConnectionHit{IP: "10.0.0.9", SeenAt: now},
		ConnectionHit{IP: "203.0.113.7", SeenAt: now},
	)
	if err := st.StampDeviceOverLimit(now); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	got, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status == model.StatusDeviceLimited {
		t.Error("a user still connected is already reported as device-limited")
	}
	if got.ActiveDevices != 2 {
		t.Errorf("active_devices = %d, want the real 2 — the operator's sharing signal "+
			"must stay honest even while nothing is being enforced", got.ActiveDevices)
	}
}
