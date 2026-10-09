package core

import "github.com/AppsGanin/rospanel/internal/model"

// hookRemindersPerPass bounds the reminders of each kind one pass sends: turning them
// on over thousands of users due at once spreads the burst over the passes that
// follow instead of landing on the endpoint in one go. The rest stay due.
const hookRemindersPerPass = 100

// hookReminders is the horizon (seconds: the first of model.HookExpireStages) and the
// quota share the webhook reminders go out at. Each is 0 while no enabled webhook
// takes its event: nothing to send, and no reason to make everyone in range a
// candidate of every pass.
func (m *Manager) hookReminders(_ *model.Settings) (horizon int64, percent int) {
	if m.webhookWanted(model.WebhookUserExpiring) {
		horizon = int64(model.HookExpireStages[0]) * 86400
	}
	if m.webhookWanted(model.WebhookUserTrafficLow) {
		percent = model.TrafficWarnPercent
	}
	return horizon, percent
}

func (m *Manager) webhookWanted(event string) bool {
	if len(m.pluginSubscribers(event)) > 0 {
		return true // a plugin takes it — the work that builds the event is needed
	}
	hooks, err := m.store.EnabledWebhooksForEvent(event)
	return err == nil && len(hooks) > 0
}

// remindHooks sends an external system its reminders: user.expiring at each of
// model.HookExpireStages once per term — only the nearest due, so a term given with
// two days left sends the 3-day stage and not every one it skipped; a renewal moves
// expire_at and starts over — and user.traffic_low once per quota
// (re-armed when usage drops back under the line — a reset, a bigger plan). The same
// bookkeeping as the Telegram warnings, on markers of their own. A disabled user is
// reminded of nothing.
func (m *Manager) remindHooks(set *model.Settings, users []model.User) {
	horizon, percent := m.hookReminders(set)
	if horizon == 0 && percent == 0 {
		return
	}
	now := m.now().Unix()
	var expiring, low []any
	for _, u := range users {
		if stage, ok := expiringStageDue(u, now, horizon); ok && len(expiring) < hookRemindersPerPass {
			// Recorded first: a failed record would repeat the reminder every pass.
			if err := m.store.SetHookExpire(u.ID, u.ExpireAt, stage); err != nil {
				logErr("reminders: recording the expiry reminder failed", "user", u.ID, "err", err)
			} else {
				d := m.userEventData(u)
				d["days_left"] = (u.ExpireAt - now + 86399) / 86400 // round up: "0 days" reads as expired
				d["stage"] = stage
				// What the bot's warning ends with: whether the balance renews the plan.
				if r := m.Renewal(set, u); r.PriceKop > 0 {
					d["auto_renew"], d["renew_price_kop"], d["balance_kop"] = r.On, r.PriceKop, r.BalanceKop
				}
				expiring = append(expiring, d)
			}
		}
		if percent == 0 {
			continue
		}
		used := u.UsedUp + u.UsedDown
		over := u.Enabled && u.DataLimit > 0 && used*100 >= u.DataLimit*int64(percent)
		switch {
		case !over && u.HookQuotaAt != 0:
			if err := m.store.SetHookQuotaAt(u.ID, 0); err != nil {
				logErr("reminders: re-arming the traffic reminder failed", "user", u.ID, "err", err)
			}
			continue
		case !over, u.HookQuotaAt != 0, len(low) >= hookRemindersPerPass:
			continue
		}
		if err := m.store.SetHookQuotaAt(u.ID, now); err != nil {
			logErr("reminders: recording the traffic reminder failed", "user", u.ID, "err", err)
			continue
		}
		// Already out: recorded so it is not looked at again, while user.limited is
		// what says so.
		if used >= u.DataLimit {
			continue
		}
		d := m.userEventData(u)
		d["used"] = used
		d["percent"] = used * 100 / u.DataLimit
		low = append(low, d)
	}
	m.EmitWebhookEach(model.WebhookUserExpiring, expiring)
	m.EmitWebhookEach(model.WebhookUserTrafficLow, low)
}

// expiringStageDue is the user.expiring stage a user is owed now, if any: inside the
// horizon, switched on, and nearer than the last stage sent for this term.
func expiringStageDue(u model.User, now, horizon int64) (int, bool) {
	left := u.ExpireAt - now
	if horizon == 0 || !u.Enabled || left <= 0 || left > horizon {
		return 0, false
	}
	stage := model.HookExpireStage(left)
	sent := 0
	if u.HookExpireAt == u.ExpireAt {
		sent = u.HookExpireStage
	}
	return stage, stage != 0 && (sent == 0 || stage < sent)
}
