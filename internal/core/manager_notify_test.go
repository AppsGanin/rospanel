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

// notifyUser creates a real user row. The transition detector now compares against
// users.notified_status (persisted, so a restart can't lose a transition), so the
// row must exist for its state to be remembered between polls.
func notifyUser(t *testing.T, m *Manager, name string) int64 {
	t.Helper()
	u, err := m.store.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return u.ID
}

// poll runs one transition check for a user in the given derived status, reading the
// persisted notified_status back from the store exactly as PollStats does.
func poll(t *testing.T, m *Manager, id int64, status string, devices ...int) {
	t.Helper()
	u, err := m.store.GetUser(id)
	if err != nil {
		t.Fatalf("get user %d: %v", id, err)
	}
	u.Status = status
	if len(devices) == 2 {
		u.ActiveDevices, u.DeviceLimit = devices[0], devices[1]
	}
	m.notifyStatusTransitions([]model.User{*u})
}

// TestNotifyStatusTransitions verifies the edge-triggered admin alerts: the first
// poll is a silent baseline, an active→terminal change fires exactly once, and the
// condition persisting does not re-alert.
func TestNotifyStatusTransitions(t *testing.T) {
	m, msgs := newNotifyManager(t)
	id := notifyUser(t, m, "alice")

	poll(t, m, id, model.StatusActive) // baseline: no alert
	if len(*msgs) != 0 {
		t.Fatalf("baseline poll alerted: %v", *msgs)
	}

	poll(t, m, id, model.StatusExpired) // active → expired: one alert
	if len(*msgs) != 1 || !strings.Contains((*msgs)[0], "истекла") {
		t.Fatalf("expected one expiry alert, got %v", *msgs)
	}

	poll(t, m, id, model.StatusExpired) // still expired: no repeat
	if len(*msgs) != 1 {
		t.Fatalf("repeat alert while condition holds: %v", *msgs)
	}

	// Re-activating then exhausting quota fires a fresh, distinct alert.
	poll(t, m, id, model.StatusActive)
	poll(t, m, id, model.StatusLimited)
	if len(*msgs) != 2 || !strings.Contains((*msgs)[1], "трафик") {
		t.Fatalf("expected a traffic-limit alert, got %v", *msgs)
	}
}

// The transition state is persisted, so a subscription that lapses while the panel
// is DOWN is still reported on the next poll after it comes back. This used to be
// lost: the detector kept the previous statuses in memory and the first poll after a
// restart could only re-baseline.
func TestNotifyStatusTransitionsSurviveRestart(t *testing.T) {
	m, msgs := newNotifyManager(t)
	id := notifyUser(t, m, "alice")
	poll(t, m, id, model.StatusActive) // seen as active, recorded
	if len(*msgs) != 0 {
		t.Fatalf("baseline alerted: %v", *msgs)
	}

	// The panel restarts: a brand-new Manager over the SAME store, with no memory of
	// the previous poll. Meanwhile the subscription lapsed.
	restarted := &Manager{store: m.store}
	var after []string
	restarted.SetAdminNotifier(func(html string) { after = append(after, html) })

	poll(t, restarted, id, model.StatusExpired)
	if len(after) != 1 || !strings.Contains(after[0], "истекла") {
		t.Fatalf("expiry during downtime went unreported: %v", after)
	}
}

// A user nobody has ever been alerted about (a fresh row, or one predating the 0020
// migration) is baselined silently — upgrading must not fire alerts for users who
// have been expired for weeks.
func TestNotifyStatusBaselinesUnknownSilently(t *testing.T) {
	m, msgs := newNotifyManager(t)
	id := notifyUser(t, m, "old")

	poll(t, m, id, model.StatusExpired) // never alerted before ⇒ silent baseline
	if len(*msgs) != 0 {
		t.Fatalf("alerted on first sight of an already-expired user: %v", *msgs)
	}
	u, _ := m.store.GetUser(id)
	if u.NotifiedStatus != model.StatusExpired {
		t.Errorf("notified_status = %q, want it baselined to expired", u.NotifiedStatus)
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

	id := notifyUser(t, m, "bob")
	poll(t, m, id, model.StatusActive)  // baseline
	poll(t, m, id, model.StatusExpired) // gated off → silent
	if len(*msgs) != 0 {
		t.Fatalf("expiry alert fired despite being disabled: %v", *msgs)
	}

	poll(t, m, id, model.StatusActive)
	poll(t, m, id, model.StatusDeviceLimited, 3, 2) // enabled category → fires
	if len(*msgs) != 1 || !strings.Contains((*msgs)[0], "устройств") {
		t.Fatalf("expected a device-limit alert, got %v", *msgs)
	}
}
