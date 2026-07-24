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

// notifyUserEvent pushes a message to one VPN user's own Telegram chat, when that
// category is enabled and their chat is linked. Separate from notifyAdminEvent
// because the audiences and the wording differ: the operator is told about somebody,
// the user is told about themselves.
func (m *Manager) notifyUserEvent(set *model.Settings, u model.User, bit int64, html string) {
	if u.TgChatID == 0 || !set.TGUserBotEnabled || !set.UserNotifyEnabled(bit) {
		return
	}
	m.notifyUser(u.TgChatID, html)
}

// notifyRegistrationDecision tells a chat the outcome of its moderated signup. It
// takes a chat id rather than a user because a rejection has no user to speak of.
func (m *Manager) notifyRegistrationDecision(chatID int64, html string) {
	set, err := m.store.GetSettings()
	if err != nil || !set.TGUserBotEnabled || !set.UserNotifyEnabled(model.UserNotifyRegistration) {
		return
	}
	m.notifyUser(chatID, html)
}

// UserNotifyPrefs returns the per-category on/off map plus the warning horizon, for
// the settings UI.
func (m *Manager) UserNotifyPrefs() (map[string]bool, int) {
	out := make(map[string]bool, len(model.UserNotifyCatalog))
	set, err := m.store.GetSettings()
	days := 3
	if err == nil {
		days = set.ExpiringDays()
	}
	for _, e := range model.UserNotifyCatalog {
		out[e.Key] = err == nil && set.UserNotifyEnabled(e.Bit)
	}
	return out, days
}

// SaveUserNotifyPrefs persists the user-facing notification categories and how many
// days ahead the expiry warning goes out.
func (m *Manager) SaveUserNotifyPrefs(prefs map[string]bool, expiringDays int) error {
	var mask int64
	for _, e := range model.UserNotifyCatalog {
		if prefs[e.Key] {
			mask |= e.Bit
		}
	}
	if expiringDays < 1 {
		expiringDays = 1
	}
	if expiringDays > 30 {
		expiringDays = 30
	}
	return m.store.SetUserEvents(mask, expiringDays)
}

// notifyExpiring warns users whose subscription runs out within the configured
// horizon. Unlike the transitions below this is not edge-triggered — nothing about a
// user changes as the date approaches — so it keys off the expiry itself: the value
// warned about is recorded, and a renewal moves expire_at, which re-arms the warning
// without any extra bookkeeping.
func (m *Manager) notifyExpiring(set *model.Settings, users []model.User) {
	if !set.TGUserBotEnabled || !set.UserNotifyEnabled(model.UserNotifyExpiring) {
		return
	}
	now := time.Now().Unix()
	horizon := int64(set.ExpiringDays()) * 86400
	for _, u := range users {
		switch {
		case u.TgChatID == 0, u.ExpireAt == 0:
			continue
		case u.ExpireAt <= now: // already gone — that is the expired notice's job
			continue
		case u.ExpireAt-now > horizon:
			continue
		case u.NotifiedExpireAt == u.ExpireAt: // already warned about this expiry
			continue
		}
		// Recorded first: a failure to send costs one warning, while a failure to
		// record would repeat it every poll.
		if err := m.store.SetNotifiedExpireAt(u.ID, u.ExpireAt); err != nil {
			logErr("notify: recording expiry warning failed", "user", u.ID, "err", err)
			continue
		}
		left := int((u.ExpireAt - now + 86399) / 86400) // round up: "0 дней" reads as expired
		m.notifyUser(u.TgChatID, fmt.Sprintf(
			"⏳ <b>Подписка заканчивается</b>\n\nОсталось %d %s — до %s.",
			left, pluralDays(left), time.Unix(u.ExpireAt, 0).In(m.Location()).Format("02.01.2006")))
	}
}

// notifyTrafficLow warns users who have spent most of their quota, while there is
// still something to do about it. The marker is cleared once usage drops back under
// the threshold, so a reset or a plan change re-arms the warning by itself.
func (m *Manager) notifyTrafficLow(set *model.Settings, users []model.User) {
	if !set.TGUserBotEnabled {
		return
	}
	for _, u := range users {
		used := u.UsedUp + u.UsedDown
		// Unlimited is "not over" rather than "skip": returning early left the marker
		// set forever on anyone moved to an unlimited plan, and a later move back to a
		// limited one — which carries usage over — then suppressed the warning for
		// good.
		over := u.DataLimit > 0 && used*100 >= u.DataLimit*int64(model.TrafficWarnPercent)
		switch {
		case !over && u.NotifiedQuotaAt != 0:
			// Back under the line — a reset or a bigger plan. Re-arm.
			if err := m.store.SetNotifiedQuotaAt(u.ID, 0); err != nil {
				logErr("notify: re-arming quota warning failed", "user", u.ID, "err", err)
			}
			continue
		case !over, u.NotifiedQuotaAt != 0:
			continue
		case u.DataLimit > 0 && used >= u.DataLimit:
			// Already out; the exhausted notice covers this and says something the
			// warning no longer can.
			continue
		}
		if u.TgChatID == 0 || !set.UserNotifyEnabled(model.UserNotifyTrafficLow) {
			continue
		}
		// Recorded first: a failed send costs one warning, a failed record repeats it
		// every poll.
		if err := m.store.SetNotifiedQuotaAt(u.ID, time.Now().Unix()); err != nil {
			logErr("notify: recording quota warning failed", "user", u.ID, "err", err)
			continue
		}
		m.notifyUser(u.TgChatID, fmt.Sprintf(
			"📊 <b>Трафик заканчивается</b>\n\nИзрасходовано %d%% — осталось %s.",
			used*100/u.DataLimit, humanBytes(u.DataLimit-used)))
	}
}

// pluralDays picks the Russian form for a day count.
func pluralDays(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return "день"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		return "дня"
	default:
		return "дней"
	}
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
	set, serr := m.store.GetSettings()
	if serr == nil {
		m.notifyExpiring(set, users)
		m.notifyTrafficLow(set, users)
	}
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
			if serr == nil {
				m.notifyUserEvent(set, u, model.UserNotifyExpired,
					"⌛ <b>Подписка истекла</b>\n\nДоступ приостановлен. Продлите подписку в этом боте, чтобы снова подключиться.")
			}
			m.auditNamed(ctx, u.ID, u.Name, model.EventUserExpired, map[string]any{"expire_at": u.ExpireAt})
			m.EmitWebhook(model.WebhookUserExpired, userEventData(u))
		case model.StatusLimited:
			m.notifyAdminEvent(model.AdminEventLimited, fmt.Sprintf(
				"📉 <b>Исчерпан трафик</b>\nПользователь: %s", escHTML(u.Name)))
			if serr == nil {
				m.notifyUserEvent(set, u, model.UserNotifyLimited,
					"📉 <b>Трафик закончился</b>\n\nДоступ приостановлен до обновления лимита или смены тарифа.")
			}
			m.auditNamed(ctx, u.ID, u.Name, model.EventUserLimited, map[string]any{
				"data_limit": u.DataLimit, "used": u.UsedUp + u.UsedDown,
			})
			m.EmitWebhook(model.WebhookUserLimited, userEventData(u))
		case model.StatusDeviceLimited:
			m.notifyAdminEvent(model.AdminEventDeviceLimited, fmt.Sprintf(
				"📵 <b>Превышен лимит устройств</b>\nПользователь: %s\nАктивных устройств: %d из %d",
				escHTML(u.Name), u.ActiveDevices, u.DeviceLimit))
			if serr == nil {
				m.notifyUserEvent(set, u, model.UserNotifyDeviceLimited, fmt.Sprintf(
					"📵 <b>Слишком много устройств</b>\n\nПодключено %d из %d. Отключите лишние — доступ восстановится сам.",
					u.ActiveDevices, u.DeviceLimit))
			}
			m.auditNamed(ctx, u.ID, u.Name, model.EventDeviceLimited, map[string]any{
				"device_limit": u.DeviceLimit, "active_devices": u.ActiveDevices,
			})
			m.EmitWebhook(model.WebhookUserDeviceLimit, userEventData(u))
		case model.StatusDisabled:
			// No admin counterpart: an operator who just switched someone off does not
			// need telling. The person on the other end does. No audit row or webhook
			// either — the action that caused this is already recorded where it
			// happened, and inventing an event here is how a manual disable ends up
			// reported to integrations as something else entirely.
			if serr == nil {
				m.notifyUserEvent(set, u, model.UserNotifyDisabled,
					"🚫 <b>Доступ приостановлен</b>\n\nОбратитесь в поддержку, если это неожиданно.")
			}
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
	m.crashAlerted = true
	m.throttleMu.Unlock()
	// Named, because the same category now reports the nodes' Xray too and an
	// unlabelled alarm in a fleet chat is a guess about which server is down.
	msg := "⚠️ <b>Xray аварийно завершился</b>\nСервер: " + model.LocalNodeName +
		"\nПроцесс перезапускается автоматически."
	if err != nil {
		msg += "\nПричина: " + escHTML(err.Error())
	}
	m.notifyAdminEvent(model.AdminEventXrayDown, msg)
}

// onXrayRecover reports that Xray is back, but only when this panel actually raised
// the alarm. An alert with no all-clear leaves the operator unable to tell "recovered
// in two seconds" from "still down" — and an all-clear for an alarm that was
// throttled away would announce the end of an outage nobody was told about.
func (m *Manager) onXrayRecover() {
	m.throttleMu.Lock()
	alerted, at := m.crashAlerted, m.lastCrashNotify
	m.crashAlerted = false
	m.throttleMu.Unlock()
	if !alerted {
		return
	}
	msg := "✅ <b>Xray снова работает</b>\nСервер: " + model.LocalNodeName
	if down := time.Since(at); down > time.Second {
		msg += fmt.Sprintf("\nПростой: %s.", fmtDowntime(down))
	}
	m.notifyAdminEvent(model.AdminEventXrayDown, msg)
}

// fmtDowntime renders an outage length the way a person would say it.
func fmtDowntime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d сек", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d ч %d мин", int(d.Hours()), int(d.Minutes())%60)
	}
}

// notifyCertRenewed reports a successful certificate renewal.
func (m *Manager) notifyCertRenewed(host string, daysLeft int) {
	m.notifyAdminEvent(model.AdminEventCert, fmt.Sprintf(
		"🔒 <b>Сертификат TLS обновлён</b>\nСервер: %s\nХост: %s\nДействует ещё %d дн.",
		model.LocalNodeName, escHTML(host), daysLeft))
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
		"🔓 <b>Не удалось обновить сертификат TLS</b>\nСервер: %s\nХост: %s\nОшибка: %s",
		model.LocalNodeName, escHTML(host), escHTML(err.Error())))
}
