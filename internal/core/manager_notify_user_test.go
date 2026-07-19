package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// userNotifyManager wires a manager whose user bot is on and whose notices are all
// enabled, capturing what would be sent.
func userNotifyManager(t *testing.T) (*Manager, *[]string) {
	t.Helper()
	m := bulkTestManager(t)
	var sent []string
	m.SetUserNotifier(func(chatID int64, html string) { sent = append(sent, html) })
	if err := m.store.SetTelegramUserBot(true, "111:AAA", model.RegOpen, ""); err != nil {
		t.Fatalf("enable user bot: %v", err)
	}
	if err := m.store.SetUserEvents(-1, 3); err != nil {
		t.Fatalf("enable notices: %v", err)
	}
	return m, &sent
}

// The warning must fire once per expiry, not once per poll — the sweep runs every
// minute, and a reminder that repeats every minute is what makes people mute the bot.
func TestExpiryWarningFiresOncePerExpiry(t *testing.T) {
	m, sent := userNotifyManager(t)
	id := mkUser(t, m, "vasya", time.Now().Add(48*time.Hour).Unix())
	if err := m.store.SetUserTelegramChat(id, 555); err != nil {
		t.Fatalf("link: %v", err)
	}

	set, _ := m.store.GetSettings()
	users, _ := m.store.ListUsers()
	m.notifyExpiring(set, users)
	if len(*sent) != 1 {
		t.Fatalf("sent %d warnings, want 1", len(*sent))
	}

	users, _ = m.store.ListUsers()
	m.notifyExpiring(set, users)
	if len(*sent) != 1 {
		t.Fatalf("warning repeated on the next poll: %d", len(*sent))
	}

	// Renewing moves the expiry, which is what re-arms the warning — no separate
	// bookkeeping, and no way to leave it stuck armed or stuck spent.
	if err := m.store.SetUserLimits(id, 0, time.Now().Add(72*time.Hour).Unix(), 0); err != nil {
		t.Fatalf("renew: %v", err)
	}
	users, _ = m.store.ListUsers()
	m.notifyExpiring(set, users)
	if len(*sent) != 2 {
		t.Fatalf("renewal did not re-arm the warning: %d", len(*sent))
	}
}

// Outside the horizon, already expired, or with no expiry at all — nothing is sent.
func TestExpiryWarningScope(t *testing.T) {
	m, sent := userNotifyManager(t)
	far := mkUser(t, m, "far", time.Now().Add(30*24*time.Hour).Unix())
	gone := mkUser(t, m, "gone", time.Now().Add(-time.Hour).Unix())
	never := mkUser(t, m, "never", 0)
	for i, id := range []int64{far, gone, never} {
		if err := m.store.SetUserTelegramChat(id, int64(600+i)); err != nil {
			t.Fatalf("link: %v", err)
		}
	}
	set, _ := m.store.GetSettings()
	users, _ := m.store.ListUsers()
	m.notifyExpiring(set, users)
	if len(*sent) != 0 {
		t.Fatalf("sent %d warnings, want none: %v", len(*sent), *sent)
	}
}

// The traffic warning re-arms itself when usage falls back under the line, which is
// what a quota reset or a bigger plan does.
func TestTrafficWarningReArmsOnReset(t *testing.T) {
	m, sent := userNotifyManager(t)
	id := mkUser(t, m, "vasya", 0)
	if err := m.store.SetUserTelegramChat(id, 555); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := m.store.SetUserLimits(id, 1000, 0, 0); err != nil {
		t.Fatalf("limits: %v", err)
	}
	if err := m.store.UpdateTraffic(id, 850, 0, 850, 0); err != nil {
		t.Fatalf("traffic: %v", err)
	}

	set, _ := m.store.GetSettings()
	users, _ := m.store.ListUsers()
	m.notifyTrafficLow(set, users)
	users, _ = m.store.ListUsers()
	m.notifyTrafficLow(set, users)
	if len(*sent) != 1 {
		t.Fatalf("sent %d warnings, want exactly 1", len(*sent))
	}

	// A reset drops usage back under the threshold.
	if err := m.store.ResetTraffic(id, 0, 0); err != nil {
		t.Fatalf("reset: %v", err)
	}
	users, _ = m.store.ListUsers()
	m.notifyTrafficLow(set, users) // re-arms, sends nothing
	if err := m.store.UpdateTraffic(id, 900, 0, 900, 0); err != nil {
		t.Fatalf("traffic: %v", err)
	}
	users, _ = m.store.ListUsers()
	m.notifyTrafficLow(set, users)
	if len(*sent) != 2 {
		t.Fatalf("warning did not re-arm after a reset: %d", len(*sent))
	}
}

// A switched-off category must silence that notice while leaving the others alone.
func TestUserNoticesRespectTheirToggles(t *testing.T) {
	m, sent := userNotifyManager(t)
	id := mkUser(t, m, "vasya", time.Now().Add(24*time.Hour).Unix())
	if err := m.store.SetUserTelegramChat(id, 555); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := m.SaveUserNotifyPrefs(map[string]bool{"expired": true}, 3); err != nil {
		t.Fatalf("prefs: %v", err)
	}
	set, _ := m.store.GetSettings()
	users, _ := m.store.ListUsers()
	m.notifyExpiring(set, users)
	if len(*sent) != 0 {
		t.Fatalf("a disabled category still wrote to the user: %v", *sent)
	}
}
