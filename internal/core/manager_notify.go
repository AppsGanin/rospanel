package core

import (
	"context"
	"fmt"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// crashNotifyThrottle / certErrNotifyThrottle bound how often a stuck condition
// (an Xray crash loop, a repeatedly-failing ACME renewal) may alert the admin
// chats, so one broken state can't flood them.
const (
	crashNotifyThrottle   = 5 * time.Minute
	certErrNotifyThrottle = 6 * time.Hour
)

// notifyAdminEvent broadcasts an HTML message (via the admin bot's notifier) to
// the authorized admin chats, but only when the given AdminEvent* category is
// enabled in settings. No-op when no admin bot is wired or the category is off.
func (m *Manager) notifyAdminEvent(bit int64, html string) {
	m.notifyMu.Lock()
	fn := m.adminNotify
	m.notifyMu.Unlock()
	if fn == nil {
		return
	}
	set, err := m.store.GetSettings()
	if err != nil || !set.AdminEventEnabled(bit) {
		return
	}
	fn(html)
}

// AdminEventPrefs returns the per-category on/off map for the settings UI.
func (m *Manager) AdminEventPrefs() map[string]bool {
	out := make(map[string]bool, len(model.AdminEventCatalog))
	set, err := m.store.GetSettings()
	for _, e := range model.AdminEventCatalog {
		out[e.Key] = err == nil && set.AdminEventEnabled(e.Bit)
	}
	return out
}

// SaveAdminEventPrefs persists the admin notification categories from the UI map.
// A key absent from the map (or false) disables that category.
func (m *Manager) SaveAdminEventPrefs(prefs map[string]bool) error {
	var mask int64
	for _, e := range model.AdminEventCatalog {
		if prefs[e.Key] {
			mask |= e.Bit
		}
	}
	return m.store.SetAdminEvents(mask)
}

// notifyStatusTransitions compares each user's freshly-derived status against the
// previous poll's snapshot and alerts the admin chats when a user crosses from
// active into a terminal state (expired / out of quota / over the device limit).
// Edge-triggered: it fires once per transition, never while the condition holds.
// The first call only records the baseline so a panel restart doesn't re-alert.
func (m *Manager) notifyStatusTransitions(users []model.User) {
	ctx := context.Background() // background poller ⇒ the system is the actor
	for _, u := range users {
		if u.NotifiedStatus == u.Status {
			continue // nothing changed since the last alert
		}
		// Record the new status FIRST. If the alert below fails (or the panel dies
		// mid-loop) we'd rather drop one notification than re-fire it every 60s.
		if err := m.store.SetNotifiedStatus(u.ID, u.Status); err != nil {
			logErr("notify: recording status failed", "user", u.ID, "err", err)
			continue
		}
		// "" = never alerted about (a fresh user, or a row predating the 0020 migration):
		// baseline it silently rather than alerting for a state that may be long-standing.
		if u.NotifiedStatus == "" {
			continue
		}
		if u.NotifiedStatus != model.StatusActive {
			continue // only transitions away from active are interesting
		}
		switch u.Status {
		case model.StatusExpired:
			m.notifyAdminEvent(model.AdminEventExpired, fmt.Sprintf(
				"⌛ <b>Подписка истекла</b>\nПользователь: %s", escHTML(u.Name)))
			m.auditNamed(ctx, u.ID, u.Name, model.EventUserExpired, map[string]any{"expire_at": u.ExpireAt})
			m.EmitWebhook(model.WebhookUserExpired, userEventData(u))
		case model.StatusLimited:
			m.notifyAdminEvent(model.AdminEventLimited, fmt.Sprintf(
				"📉 <b>Исчерпан трафик</b>\nПользователь: %s", escHTML(u.Name)))
			m.auditNamed(ctx, u.ID, u.Name, model.EventUserLimited, map[string]any{
				"data_limit": u.DataLimit, "used": u.UsedUp + u.UsedDown,
			})
			m.EmitWebhook(model.WebhookUserLimited, userEventData(u))
		case model.StatusDeviceLimited:
			m.notifyAdminEvent(model.AdminEventDeviceLimited, fmt.Sprintf(
				"📵 <b>Превышен лимит устройств</b>\nПользователь: %s\nАктивных устройств: %d из %d",
				escHTML(u.Name), u.ActiveDevices, u.DeviceLimit))
			m.auditNamed(ctx, u.ID, u.Name, model.EventDeviceLimited, map[string]any{
				"device_limit": u.DeviceLimit, "active_devices": u.ActiveDevices,
			})
			m.EmitWebhook(model.WebhookUserDeviceLimit, userEventData(u))
		}
	}
}

// onXrayCrash alerts the admin chats that the Xray child exited unexpectedly and
// is being restarted. Throttled so a crash loop sends at most one alert per
// crashNotifyThrottle. Invoked from the supervisor's monitor goroutine.
func (m *Manager) onXrayCrash(err error) {
	m.throttleMu.Lock()
	now := time.Now()
	if now.Sub(m.lastCrashNotify) < crashNotifyThrottle {
		m.throttleMu.Unlock()
		return
	}
	m.lastCrashNotify = now
	m.throttleMu.Unlock()
	msg := "⚠️ <b>Xray аварийно завершился</b>\nПроцесс перезапускается автоматически."
	if err != nil {
		msg += "\nПричина: " + escHTML(err.Error())
	}
	m.notifyAdminEvent(model.AdminEventXrayDown, msg)
}

// notifyCertRenewed reports a successful certificate renewal.
func (m *Manager) notifyCertRenewed(host string, daysLeft int) {
	m.notifyAdminEvent(model.AdminEventCert, fmt.Sprintf(
		"🔒 <b>Сертификат TLS обновлён</b>\nХост: %s\nДействует ещё %d дн.", escHTML(host), daysLeft))
}

// notifyCertError reports a failed ACME renewal, throttled so the fast retry
// cadence (every few minutes while no valid cert exists) can't flood the chats.
func (m *Manager) notifyCertError(host string, err error) {
	m.throttleMu.Lock()
	now := time.Now()
	if now.Sub(m.lastCertErrNotify) < certErrNotifyThrottle {
		m.throttleMu.Unlock()
		return
	}
	m.lastCertErrNotify = now
	m.throttleMu.Unlock()
	m.notifyAdminEvent(model.AdminEventCert, fmt.Sprintf(
		"🔓 <b>Не удалось обновить сертификат TLS</b>\nХост: %s\nОшибка: %s", escHTML(host), escHTML(err.Error())))
}
