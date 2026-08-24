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

// The scenario from issue #66: a phone with a one-device limit moves from mobile data to
// Wi-Fi. Its old address is still inside the two-minute window, so the address counter
// sees two devices — while the HWID roster, which is what actually caps the account, sees
// one. The user was dropped from the generated config and told they had exceeded the
// limit, for a network change.
func TestNetworkSwitchDoesNotTripTheLimitWhenHWIDIsAuthoritative(t *testing.T) {
	st := dcStore(t)
	u, err := st.CreateUser("mobile", "uuid", "pw", "tok", 0, 0, 1) // one device
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Now().Unix()
	// Two addresses seen inside the window: the same phone, before and after the switch.
	if err := st.AddConnections([]ConnectionHit{
		{UserID: u.ID, IP: "10.0.0.1", SeenAt: now - 30, Hits: 1},
		{UserID: u.ID, IP: "192.168.1.5", SeenAt: now, Hits: 1},
	}); err != nil {
		t.Fatalf("connections: %v", err)
	}

	working := func() bool {
		us, err := st.WorkingUsers(now)
		if err != nil {
			t.Fatalf("working: %v", err)
		}
		for _, w := range us {
			if w.ID == u.ID {
				return true
			}
		}
		return false
	}
	status := func() string {
		got, err := st.GetUser(u.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		return got.Status
	}

	// Historical behaviour, and still the answer when addresses are all we have.
	if working() {
		t.Error("with only the address counter, two addresses must trip a one-device limit")
	}
	if status() != model.StatusDeviceLimited {
		t.Errorf("status = %q, want %q", status(), model.StatusDeviceLimited)
	}

	// Turn HWID binding on and require it: every served client is now identified by
	// hardware id, so counting addresses only invents devices.
	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	set.HWIDEnabled, set.HWIDRequire = true, true
	if err := st.SetHWIDSettings(set); err != nil {
		t.Fatalf("hwid on: %v", err)
	}

	if !working() {
		t.Error("a phone that changed network is still cut out of the config")
	}
	if status() == model.StatusDeviceLimited {
		t.Error("the bot is still told the device limit is exceeded")
	}

	// An operator who wants the old behaviour back can say so.
	if err := st.SetDeviceCountMode(model.DeviceCountBoth); err != nil {
		t.Fatalf("mode both: %v", err)
	}
	if working() {
		t.Error(`mode "both" must keep enforcing the address counter`)
	}

	// And one who never wants addresses to count can say that too.
	if err := st.SetDeviceCountMode(model.DeviceCountHWID); err != nil {
		t.Fatalf("mode hwid: %v", err)
	}
	if !working() {
		t.Error(`mode "hwid" must ignore the address counter`)
	}
}

// The SQL rule and the Go rule are written twice, so they are pinned to agree.
func TestDeviceCountRuleAgreesAcrossSQLAndGo(t *testing.T) {
	st := dcStore(t)
	for _, tc := range []struct {
		mode              string
		enabled, required bool
	}{
		{model.DeviceCountAuto, false, false},
		{model.DeviceCountAuto, true, false},
		{model.DeviceCountAuto, true, true},
		{model.DeviceCountAuto, false, true},
		{model.DeviceCountHWID, true, true},
		{model.DeviceCountHWID, false, false},
		{model.DeviceCountBoth, true, true},
	} {
		set, err := st.GetSettings()
		if err != nil {
			t.Fatalf("settings: %v", err)
		}
		set.HWIDEnabled, set.HWIDRequire = tc.enabled, tc.required
		if err := st.SetHWIDSettings(set); err != nil {
			t.Fatalf("hwid: %v", err)
		}
		if err := st.SetDeviceCountMode(tc.mode); err != nil {
			t.Fatalf("mode: %v", err)
		}
		set, _ = st.GetSettings()
		if got, want := st.ipCountsAsDevice(), set.CountsIPAsDevice(); got != want {
			t.Errorf("mode=%s enabled=%v required=%v: SQL says %v, Go says %v",
				tc.mode, tc.enabled, tc.required, got, want)
		}
	}
}
