package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func dcStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "dc.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// dcUser makes a one-device account and records its sightings.
func dcUser(t *testing.T, st *Store, name string, hits ...ConnectionHit) model.User {
	t.Helper()
	u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 1)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	for i := range hits {
		hits[i].UserID = u.ID
		hits[i].Hits = 1
	}
	if err := st.AddConnections(hits); err != nil {
		t.Fatalf("connections: %v", err)
	}
	return *u
}

func dcWorking(t *testing.T, st *Store, id int64, now int64) bool {
	t.Helper()
	us, err := st.WorkingUsers(now)
	if err != nil {
		t.Fatalf("working: %v", err)
	}
	for _, w := range us {
		if w.ID == id {
			return true
		}
	}
	return false
}

// A device that has gone quiet still counts until it leaves the window. This is the
// property that catches a shared credential, and it is easy to lose: access-log lines are
// written per newly accepted connection, so a device between bursts produces nothing at
// all. A rule that drops an address after a few quiet seconds — a "handover grace"
// measured against the user's newest sighting was one, and shipped briefly — lets any
// number of devices through a one-device limit simply by taking turns.
func TestQuietAddressesStillCountInsideTheWindow(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()

	// Two devices genuinely online, one of them 40s between bursts.
	shared := dcUser(t, st, "shared",
		ConnectionHit{IP: "10.0.0.9", SeenAt: now - 40},
		ConnectionHit{IP: "203.0.113.7", SeenAt: now},
	)
	if dcWorking(t, st, shared.ID, now) {
		t.Error("a device quiet for 40s stopped counting: two addresses no longer trip " +
			"a one-device limit")
	}

	// Five devices taking turns, none of them recent except the last. The grace scored
	// this as one device.
	ring := dcUser(t, st, "ring",
		ConnectionHit{IP: "10.1.0.1", SeenAt: now - 100},
		ConnectionHit{IP: "10.1.0.2", SeenAt: now - 75},
		ConnectionHit{IP: "10.1.0.3", SeenAt: now - 50},
		ConnectionHit{IP: "10.1.0.4", SeenAt: now - 25},
		ConnectionHit{IP: "10.1.0.5", SeenAt: now},
	)
	if dcWorking(t, st, ring.ID, now) {
		t.Error("five addresses taking turns passed a one-device limit")
	}
	got, err := st.GetUser(ring.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ActiveDevices != 5 {
		t.Errorf("panel shows %d devices for five addresses in the window, want 5 — "+
			"the operator's only sharing signal has to be the real number", got.ActiveDevices)
	}
}

// The known false positive from issue #66, pinned deliberately rather than left to be
// rediscovered: a phone changing network abandons its old address, but that address keeps
// a fresh last_seen until it leaves the window, so a user on exactly their allowance is
// cut for up to DeviceOnlineWindow.
//
// This is a documented trade, not an oversight. Forgiving the stale address cannot be
// done from this table: cutting the user is what stops their sightings, so any reference
// the forgiveness measures against freezes at the moment of the cut. An operator who
// wants it gone chooses "hwid" and gives up the concurrency counter with it — see the
// mode test below.
func TestHandoverIsStillCountedInAutoMode(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()
	phone := dcUser(t, st, "phone",
		ConnectionHit{IP: "10.0.0.1", SeenAt: now - 60},
		ConnectionHit{IP: "192.168.1.5", SeenAt: now},
	)
	if dcWorking(t, st, phone.ID, now) {
		t.Error("the address a phone moved off no longer counts — if this is deliberate, " +
			"check it against TestQuietAddressesStillCountInsideTheWindow first")
	}
	// It clears itself once the abandoned address leaves the window, without anyone
	// intervening.
	if !dcWorking(t, st, phone.ID, now+model.DeviceOnlineWindow) {
		t.Error("the handover never clears on its own")
	}
}

// "hwid" gives up the address counter entirely, which is an operator's explicit choice
// and the answer to issue #66. "both" predates the removal of the handover grace and now
// behaves as "auto"; rows and API clients still carry it.
func TestDeviceCountModesBehaveAsDocumented(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()
	phone := dcUser(t, st, "phone",
		ConnectionHit{IP: "10.0.0.1", SeenAt: now - 60},
		ConnectionHit{IP: "192.168.1.5", SeenAt: now},
	)

	if err := st.SetDeviceCountMode(model.DeviceCountBoth); err != nil {
		t.Fatalf("both: %v", err)
	}
	if dcWorking(t, st, phone.ID, now) {
		t.Error(`"both" must count every address in the window, exactly as "auto" does`)
	}

	if err := st.SetDeviceCountMode(model.DeviceCountHWID); err != nil {
		t.Fatalf("hwid: %v", err)
	}
	if !dcWorking(t, st, phone.ID, now) {
		t.Error(`"hwid" must ignore addresses entirely`)
	}
	// The panel must stop calling them device-limited too, or the bot quotes a limit
	// nothing is enforcing.
	got, err := st.GetUser(phone.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status == model.StatusDeviceLimited {
		t.Error(`"hwid" still reports the address limit as exceeded`)
	}
	// And it must ignore them even for a credential in obvious simultaneous use — that
	// is the trade the operator accepts by picking it.
	shared := dcUser(t, st, "shared",
		ConnectionHit{IP: "10.0.0.9", SeenAt: now},
		ConnectionHit{IP: "203.0.113.7", SeenAt: now},
	)
	if !dcWorking(t, st, shared.ID, now) {
		t.Error(`"hwid" still enforced the address counter`)
	}
}

// The rule is written twice — SQL for the query, Go for everything else. They are pinned
// to agree, including on values neither constant covers.
func TestDeviceCountRuleAgreesAcrossSQLAndGo(t *testing.T) {
	st := dcStore(t)
	for _, mode := range []string{
		model.DeviceCountAuto, model.DeviceCountHWID, model.DeviceCountBoth,
		"", "legacy-value-from-an-older-row",
	} {
		if err := st.SetDeviceCountMode(mode); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		set, err := st.GetSettings()
		if err != nil {
			t.Fatalf("settings: %v", err)
		}
		if got, want := st.ipCountsAsDevice(), set.CountsIPAsDevice(); got != want {
			t.Errorf("mode %q: SQL says ip_counts=%v, Go says %v", mode, got, want)
		}
	}
}
