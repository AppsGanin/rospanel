package core

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// newNotifyManager builds a minimal Manager wired to a fresh store and a capturing
// admin notifier — enough to exercise the notification gating/transition logic
// without a running Xray supervisor.
func newNotifyManager(t *testing.T) (*Manager, *[]string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var msgs []string
	m := &Manager{store: st}
	m.SetAdminNotifier(func(html string) { msgs = append(msgs, html) })
	return m, &msgs
}

// TestNotifyStatusTransitions verifies the edge-triggered admin alerts: the first
// poll is a silent baseline, an active→terminal change fires exactly once, and the
// condition persisting does not re-alert.
func TestNotifyStatusTransitions(t *testing.T) {
	m, msgs := newNotifyManager(t)

	u := model.User{ID: 1, Name: "alice", Status: model.StatusActive}
	m.notifyStatusTransitions([]model.User{u}) // baseline: no alert
	if len(*msgs) != 0 {
		t.Fatalf("baseline poll alerted: %v", *msgs)
	}

	u.Status = model.StatusExpired
	m.notifyStatusTransitions([]model.User{u}) // active → expired: one alert
	if len(*msgs) != 1 || !strings.Contains((*msgs)[0], "истекла") {
		t.Fatalf("expected one expiry alert, got %v", *msgs)
	}

	m.notifyStatusTransitions([]model.User{u}) // still expired: no repeat
	if len(*msgs) != 1 {
		t.Fatalf("repeat alert while condition holds: %v", *msgs)
	}

	// Re-activating then exhausting quota fires a fresh, distinct alert.
	u.Status = model.StatusActive
	m.notifyStatusTransitions([]model.User{u})
	u.Status = model.StatusLimited
	m.notifyStatusTransitions([]model.User{u})
	if len(*msgs) != 2 || !strings.Contains((*msgs)[1], "трафик") {
		t.Fatalf("expected a traffic-limit alert, got %v", *msgs)
	}
}

// TestNotifyStatusTransitionsGated verifies a disabled category is suppressed
// while other categories still fire.
func TestNotifyStatusTransitionsGated(t *testing.T) {
	m, msgs := newNotifyManager(t)
	// Enable only device-limit alerts; expiry must be suppressed.
	if err := m.store.SetAdminEvents(model.AdminEventDeviceLimited); err != nil {
		t.Fatalf("SetAdminEvents: %v", err)
	}

	u := model.User{ID: 1, Name: "bob", Status: model.StatusActive}
	m.notifyStatusTransitions([]model.User{u}) // baseline
	u.Status = model.StatusExpired
	m.notifyStatusTransitions([]model.User{u}) // gated off → silent
	if len(*msgs) != 0 {
		t.Fatalf("expiry alert fired despite being disabled: %v", *msgs)
	}

	u.Status = model.StatusActive
	m.notifyStatusTransitions([]model.User{u})
	u.Status, u.ActiveDevices, u.DeviceLimit = model.StatusDeviceLimited, 3, 2
	m.notifyStatusTransitions([]model.User{u}) // enabled category → fires
	if len(*msgs) != 1 || !strings.Contains((*msgs)[0], "устройств") {
		t.Fatalf("expected a device-limit alert, got %v", *msgs)
	}
}
