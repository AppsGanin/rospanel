package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// TestCountUsersMatchesDeriveStatus pins the aggregate to the row-by-row logic it
// replaced. CountUsers spells deriveStatus's "active" case out in SQL, so the two
// live in different languages and can drift apart silently — a drift that would
// quietly misreport the dashboard's headline number. This walks a matrix covering
// every branch of deriveStatus and asserts both agree on the count.
func TestCountUsersMatchesDeriveStatus(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "count.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	now := time.Now().Unix()
	type spec struct {
		name        string
		enabled     bool
		dataLimit   int64
		usedUp      int64
		usedDown    int64
		expireAt    int64
		deviceLimit int
		devices     int // distinct IPs seen inside DeviceOnlineWindow
	}
	specs := []spec{
		{name: "plain active", enabled: true},
		{name: "disabled", enabled: false},
		{name: "expired", enabled: true, expireAt: now - 60},
		{name: "expiring later", enabled: true, expireAt: now + 3600},
		{name: "no expiry", enabled: true, expireAt: 0},
		{name: "quota exhausted", enabled: true, dataLimit: 100, usedUp: 60, usedDown: 40},
		{name: "quota exceeded", enabled: true, dataLimit: 100, usedUp: 500, usedDown: 500},
		{name: "quota inside", enabled: true, dataLimit: 100, usedUp: 40, usedDown: 40},
		{name: "unlimited quota", enabled: true, dataLimit: 0, usedUp: 1 << 40},
		{name: "devices under cap", enabled: true, deviceLimit: 3, devices: 2},
		{name: "devices at cap", enabled: true, deviceLimit: 3, devices: 3},
		{name: "devices over cap", enabled: true, deviceLimit: 3, devices: 4},
		{name: "no device cap", enabled: true, deviceLimit: 0, devices: 9},
		{name: "disabled and expired", enabled: false, expireAt: now - 60},
		{name: "expired and over quota", enabled: true, expireAt: now - 60, dataLimit: 10, usedUp: 99},
	}

	var wantActive, wantOnline int
	var wantUp, wantDown int64
	for i, sp := range specs {
		u, err := st.CreateUser(sp.name, fmt.Sprintf("uuid-%d", i), "pw",
			fmt.Sprintf("tok-%d", i), sp.dataLimit, sp.expireAt, sp.deviceLimit)
		if err != nil {
			t.Fatalf("create %q: %v", sp.name, err)
		}
		if !sp.enabled {
			if err := st.SetUserEnabled(u.ID, false); err != nil {
				t.Fatalf("disable %q: %v", sp.name, err)
			}
		}
		if sp.usedUp != 0 || sp.usedDown != 0 {
			if err := st.AddUsedTraffic(u.ID, sp.usedUp, sp.usedDown); err != nil {
				t.Fatalf("traffic %q: %v", sp.name, err)
			}
		}
		for d := 0; d < sp.devices; d++ {
			// Inside DeviceOnlineWindow, so these count as active devices.
			if err := st.AddConnection(u.ID, fmt.Sprintf("10.0.%d.%d", i, d), now); err != nil {
				t.Fatalf("conn %q: %v", sp.name, err)
			}
		}

		// The reference answer, from the very function CountUsers mirrors.
		if deriveStatus(sp.enabled, sp.expireAt, sp.usedUp+sp.usedDown, sp.dataLimit,
			now, sp.devices, sp.deviceLimit) == model.StatusActive {
			wantActive++
		}
		if sp.devices > 0 {
			wantOnline++
		}
		wantUp += sp.usedUp
		wantDown += sp.usedDown
	}

	// Stamp the over-limit users as having been over since before DeviceLimitGrace
	// expired, so the device dimension is actually exercised here: without it nobody is
	// cut yet and this test would agree with itself for the wrong reason.
	if err := st.StampDeviceOverLimit(now - model.DeviceLimitGrace - 10); err != nil {
		t.Fatalf("stamp: %v", err)
	}

	got, err := st.CountUsers(now)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got.Total != len(specs) {
		t.Errorf("total = %d, want %d", got.Total, len(specs))
	}
	if got.Active != wantActive {
		t.Errorf("active = %d, want %d (deriveStatus disagrees with the SQL)", got.Active, wantActive)
	}
	if got.TotalUp != wantUp || got.TotalDown != wantDown {
		t.Errorf("traffic = %d/%d, want %d/%d", got.TotalUp, got.TotalDown, wantUp, wantDown)
	}
	// Online is about the wire, not entitlement: everyone with a live device counts,
	// including the users who are over quota or expired — they were carrying packets
	// a moment ago, and the dashboard says so.
	if got.Online != wantOnline {
		t.Errorf("online = %d, want %d", got.Online, wantOnline)
	}

	// And cross-check against the slice path itself, which is what the dashboard
	// used to fold over.
	users, err := st.ListUsers()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listActive int
	for _, u := range users {
		if u.Status == model.StatusActive {
			listActive++
		}
	}
	if listActive != got.Active {
		t.Errorf("ListUsers says %d active, CountUsers says %d", listActive, got.Active)
	}
}

// TestCountUsersEmpty guards the COALESCEs: no users at all must read as zeroes,
// not a scan error on NULL sums.
func TestCountUsersEmpty(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	got, err := st.CountUsers(time.Now().Unix())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got != (UserCounts{}) {
		t.Fatalf("empty db counted %+v, want all zeroes", got)
	}
}

// TestCountUsersOnlineWindow pins what the dashboard's "Online" figure means: a
// user is online while any of their addresses was seen inside DeviceOnlineWindow,
// counted once however many devices they are on, and they drop off the moment the
// last sighting ages past the window.
func TestCountUsersOnlineWindow(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "online.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	now := time.Now().Unix()
	mk := func(name string) int64 {
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		return u.ID
	}

	twoDevices := mk("two devices")
	if err := st.AddConnection(twoDevices, "10.0.0.1", now); err != nil {
		t.Fatalf("conn: %v", err)
	}
	if err := st.AddConnection(twoDevices, "10.0.0.2", now-1); err != nil {
		t.Fatalf("conn: %v", err)
	}
	// Right on the edge of the window — still inside it.
	edge := mk("edge")
	if err := st.AddConnection(edge, "10.0.1.1", now-model.DeviceOnlineWindow+1); err != nil {
		t.Fatalf("conn: %v", err)
	}
	// One second past it: gone from the count, still in the history.
	stale := mk("stale")
	if err := st.AddConnection(stale, "10.0.2.1", now-model.DeviceOnlineWindow-1); err != nil {
		t.Fatalf("conn: %v", err)
	}
	mk("never connected")

	got, err := st.CountUsers(now)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got.Total != 4 {
		t.Fatalf("total = %d, want 4", got.Total)
	}
	if got.Online != 2 {
		t.Errorf("online = %d, want 2 (the two-device user counted once, plus the edge)", got.Online)
	}
}

// CountUsers keeps its own copy of the device clause, so it has to be pinned against the
// list beside it in every mode — not just the default. It once honoured neither the
// count mode nor the grace, which made the dashboard's "active" total disagree with the
// list rendered next to it by one user per affected account.
func TestCountUsersAgreesInEveryDeviceMode(t *testing.T) {
	for _, mode := range []string{model.DeviceCountAuto, model.DeviceCountHWID, model.DeviceCountBoth} {
		t.Run(mode, func(t *testing.T) {
			st := dcStore(t)
			now := time.Now().Unix()
			// One account well over a one-device limit, stamped from before the grace
			// expired, so the device dimension is live rather than merely present.
			dcUser(t, st, "shared",
				ConnectionHit{IP: "10.0.0.1", SeenAt: now},
				ConnectionHit{IP: "10.0.0.2", SeenAt: now},
				ConnectionHit{IP: "10.0.0.3", SeenAt: now},
			)
			if err := st.StampDeviceOverLimit(now - model.DeviceLimitGrace - 10); err != nil {
				t.Fatalf("stamp: %v", err)
			}
			// The mode is set AFTER the stamp on purpose. Switching to "hwid" clears
			// stamps, but only when something next runs the stamp — until then the row
			// carries an armed stamp under a mode that does not enforce it, and that is
			// the exact state where a clause missing the mode reads differently from a
			// clause that has it. Setting the mode first would leave device_over_since
			// at zero, which makes both readings agree for a reason that has nothing to
			// do with what this test is for.
			if err := st.SetDeviceCountMode(mode); err != nil {
				t.Fatalf("mode: %v", err)
			}
			// A second account over the limit but only just — still inside the grace,
			// so nothing has happened to it yet. This is the half that pins the grace
			// term rather than the mode term: without it, a clause that cuts the moment
			// someone goes over reads the same as one that waits.
			fresh := dcUser(t, st, "fresh",
				ConnectionHit{IP: "10.9.0.1", SeenAt: now},
				ConnectionHit{IP: "10.9.0.2", SeenAt: now},
			)
			if _, err := st.db.Exec(
				`UPDATE users SET device_over_since = ? WHERE id = ?`, now, fresh.ID); err != nil {
				t.Fatalf("arm: %v", err)
			}
			got, err := st.CountUsers(now)
			if err != nil {
				t.Fatalf("count: %v", err)
			}
			users, err := st.ListUsers()
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			var listActive int
			for _, u := range users {
				if u.Status == model.StatusActive {
					listActive++
				}
			}
			if listActive != got.Active {
				t.Errorf("ListUsers says %d active, CountUsers says %d", listActive, got.Active)
			}
		})
	}
}
