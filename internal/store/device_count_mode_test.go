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

// The report behind issue #66: a phone with a one-device limit moves from mobile data to
// Wi-Fi. It abandons the old address instantly, but that address keeps a fresh last_seen
// for the rest of the two-minute window, so it counted as a second device and the user
// dropped out of the generated config.
//
// The paired case is what makes this a real test: two addresses BOTH still in use must
// still trip the limit. Forgiving a handover must not become forgiving a shared link.
func TestHandoverIsForgivenButSharingIsNot(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()

	// One phone, mid-handover: the old address stopped being seen a minute ago.
	phone := dcUser(t, st, "phone",
		ConnectionHit{IP: "10.0.0.1", SeenAt: now - 60},
		ConnectionHit{IP: "192.168.1.5", SeenAt: now},
	)
	// Two devices genuinely online: both addresses seen just now.
	shared := dcUser(t, st, "shared",
		ConnectionHit{IP: "10.0.0.9", SeenAt: now - 2},
		ConnectionHit{IP: "203.0.113.7", SeenAt: now},
	)

	if !dcWorking(t, st, phone.ID, now) {
		t.Error("a phone that changed network is still cut out of the config")
	}
	if dcWorking(t, st, shared.ID, now) {
		t.Error("two addresses in simultaneous use no longer trip a one-device limit — " +
			"forgiving a handover must not forgive a shared credential")
	}
}

// The status the bot quotes must follow the same rule as the enforcement.
func TestHandoverDoesNotShowAsDeviceLimited(t *testing.T) {
	st := dcStore(t)
	now := time.Now().Unix()
	phone := dcUser(t, st, "phone",
		ConnectionHit{IP: "10.0.0.1", SeenAt: now - 60},
		ConnectionHit{IP: "192.168.1.5", SeenAt: now},
	)
	got, err := st.GetUser(phone.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status == model.StatusDeviceLimited {
		t.Error("the bot is told the device limit is exceeded for a network change")
	}
}

// "both" is the historical behaviour and must stay strict; "hwid" gives up the address
// counter entirely, which is an operator's explicit choice.
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
		t.Error(`"both" must count every address in the window, handover included`)
	}

	if err := st.SetDeviceCountMode(model.DeviceCountHWID); err != nil {
		t.Fatalf("hwid: %v", err)
	}
	if !dcWorking(t, st, phone.ID, now) {
		t.Error(`"hwid" must ignore addresses entirely`)
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
