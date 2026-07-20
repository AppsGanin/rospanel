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

	var wantActive int
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
		wantUp += sp.usedUp
		wantDown += sp.usedDown
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
