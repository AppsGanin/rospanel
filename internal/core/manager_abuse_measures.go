package core

import (
	"context"
	"fmt"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// What the panel does about blocklist traffic on its own — the ladder in
// model.AbuseMeasures. The operator's alert (alertAbuse) says "look at this
// account"; these say what was done while they were not looking.
//
// Every rung is idempotent against the user's recorded measure, so a day's traffic
// arriving over many flushes escalates at most once per rung and never repeats a
// rung already in force. A measure holds for the configured hours and is lifted
// by the panel; an operator who switches the user back on or resets their speed
// by hand overrules it, and the panel forgets it rather than lifting it later
// into a state the operator did not choose.

// AbuseMeasures returns the ladder for the settings UI.
func (m *Manager) AbuseMeasures() model.AbuseMeasures {
	set, err := m.store.GetSettings()
	if err != nil {
		return model.AbuseMeasures{}
	}
	return set.AbuseMeasures
}

// SetAbuseMeasures persists the ladder after validating it.
func (m *Manager) SetAbuseMeasures(a model.AbuseMeasures) error {
	if err := a.Validate(); err != nil {
		return err
	}
	return m.store.SetAbuseMeasures(a)
}

// applyAbuseMeasure takes the highest rung `total` (one user's matches for `day`)
// has reached, unless that rung — or a higher one — is already in force.
func (m *Manager) applyAbuseMeasure(set *model.Settings, userID int64, day string, total int64) {
	a := set.AbuseMeasures
	if !a.Active() {
		return
	}
	u, err := m.store.GetUser(userID)
	if err != nil || u == nil {
		return
	}
	ctx := context.Background() // the panel acts on its own: the system is the actor
	now := time.Now()
	until := now.Add(time.Duration(a.Hours) * time.Hour).Unix()

	switch {
	case a.DisableMin > 0 && total >= int64(a.DisableMin):
		if u.AbuseAction == model.AbuseActionDisable {
			return // already off for this
		}
		if !u.Enabled {
			return // switched off by the operator; nothing to add, and nothing to lift later
		}
		// A throttle being escalated gives the cap back now: the switch-off is the
		// whole measure from here, and the lift then has exactly one thing to undo.
		if u.AbuseAction == model.AbuseActionThrottle {
			if err := m.store.SetUserSpeedLimit(u.ID, u.AbusePrevSpeed); err != nil {
				logErr("abuse: restoring speed before switch-off failed", "user", u.ID, "err", err)
				return
			}
			go m.ApplyShaping()
		}
		err := m.mutateUser(fmt.Sprintf("user %d switched off for blocklist traffic (%d matches)", u.ID, total), func() error {
			if err := m.store.SetUserEnabled(u.ID, false); err != nil {
				return err
			}
			return m.store.SetAbuseMeasure(u.ID, model.AbuseActionDisable, until, 0)
		})
		if err != nil {
			return
		}
		m.auditNamed(ctx, u.ID, u.Name, model.EventAbuseDisabled,
			map[string]any{"matches": total, "day": day, "until": until, "hours": a.Hours})
		m.notifyAbuseUser(set, *u, i18n.T(m.userLang(u.TgChatID), "notify.userAbuseDisabled", a.Hours))
		m.notifyAdminEvent(model.AdminEventAbuse,
			i18n.T(m.botLang(), "notify.abuseDisabled", escHTML(u.Name), total, a.Hours))

	case a.ThrottleMin > 0 && total >= int64(a.ThrottleMin):
		if u.AbuseAction != "" {
			return // throttled already, or off — which is more
		}
		if u.SpeedLimit > 0 && u.SpeedLimit <= a.ThrottleKbps {
			return // already slower than the throttle would make them
		}
		if err := m.store.SetUserSpeedLimit(u.ID, a.ThrottleKbps); err != nil {
			logErr("abuse: throttle failed", "user", u.ID, "err", err)
			return
		}
		if err := m.store.SetAbuseMeasure(u.ID, model.AbuseActionThrottle, until, u.SpeedLimit); err != nil {
			logErr("abuse: recording the throttle failed", "user", u.ID, "err", err)
			return
		}
		logInfo("abuse: user throttled for blocklist traffic", "user", u.ID, "matches", total, "kbps", a.ThrottleKbps)
		go m.ApplyShaping()
		m.TriggerUserSync() // nodes shape from the limits in their sync payload
		m.auditNamed(ctx, u.ID, u.Name, model.EventAbuseThrottled,
			map[string]any{"matches": total, "day": day, "until": until, "hours": a.Hours,
				"speed_limit": a.ThrottleKbps, "was": u.SpeedLimit})
		m.notifyAbuseUser(set, *u, i18n.T(m.userLang(u.TgChatID), "notify.userAbuseThrottled",
			speedLabel(m.userLang(u.TgChatID), a.ThrottleKbps), a.Hours))
		m.notifyAdminEvent(model.AdminEventAbuse,
			i18n.T(m.botLang(), "notify.abuseThrottled", escHTML(u.Name), total,
				speedLabel(m.botLang(), a.ThrottleKbps), a.Hours))

	case a.WarnMin > 0 && total >= int64(a.WarnMin):
		if u.AbuseWarnedDay == day || u.AbuseAction != "" {
			return // warned today already, or past warning
		}
		if err := m.store.SetAbuseWarnedDay(u.ID, day); err != nil {
			logErr("abuse: recording the warning failed", "user", u.ID, "err", err)
			return
		}
		m.auditNamed(ctx, u.ID, u.Name, model.EventAbuseWarned, map[string]any{"matches": total, "day": day})
		m.notifyAbuseUser(set, *u, i18n.T(m.userLang(u.TgChatID), "notify.userAbuseWarned"))
	}
}

// LiftAbuseMeasures undoes every measure whose time is up. Driven by the traffic
// poll, so a measure lifts within a minute of its deadline.
func (m *Manager) LiftAbuseMeasures(now int64) {
	due, err := m.store.AbuseMeasuresDue(now)
	if err != nil {
		logErr("abuse: reading due measures failed", "err", err)
		return
	}
	if len(due) == 0 {
		return
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return
	}
	ctx := context.Background()
	for _, u := range due {
		m.liftAbuseMeasure(ctx, set, u, "expired")
	}
	m.TriggerUserSync()
	go m.ApplyShaping()
}

// liftAbuseMeasure puts back what one measure changed and forgets it. `why` goes
// into the journal: the deadline passed, or an operator overruled it.
func (m *Manager) liftAbuseMeasure(ctx context.Context, set *model.Settings, u model.User, why string) {
	switch u.AbuseAction {
	case model.AbuseActionDisable:
		if !u.Enabled {
			if err := m.store.SetUserEnabled(u.ID, true); err != nil {
				logErr("abuse: re-enabling failed", "user", u.ID, "err", err)
				return
			}
		}
	case model.AbuseActionThrottle:
		if err := m.store.SetUserSpeedLimit(u.ID, u.AbusePrevSpeed); err != nil {
			logErr("abuse: restoring speed failed", "user", u.ID, "err", err)
			return
		}
	default:
		return
	}
	if err := m.store.ClearAbuseMeasure(u.ID); err != nil {
		logErr("abuse: clearing the measure failed", "user", u.ID, "err", err)
		return
	}
	logInfo("abuse: measure lifted", "user", u.ID, "measure", u.AbuseAction, "why", why)
	m.auditNamed(ctx, u.ID, u.Name, model.EventAbuseLifted, map[string]any{"measure": u.AbuseAction, "why": why})
	if why == "expired" {
		m.notifyAbuseUser(set, u, i18n.T(m.userLang(u.TgChatID), "notify.userAbuseLifted"))
		m.notifyAdminEvent(model.AdminEventAbuse, i18n.T(m.botLang(), "notify.abuseLifted", escHTML(u.Name)))
	}
}

// overruleAbuseMeasure is called from the operator's own setters: a user switched
// back on, or given a speed, by hand is no longer under the panel's measure.
// Nothing is restored here — the operator just chose the state — only forgotten.
func (m *Manager) overruleAbuseMeasure(ctx context.Context, u *model.User, measure string) {
	if u == nil || u.AbuseAction != measure {
		return
	}
	if err := m.store.ClearAbuseMeasure(u.ID); err != nil {
		logErr("abuse: clearing the overruled measure failed", "user", u.ID, "err", err)
		return
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventAbuseLifted, map[string]any{"measure": measure, "why": "overruled"})
}

// notifyAbuseUser tells the user what was done to them, through their own bot.
// Not one of the UserNotify* categories: an operator who switched a measure on
// has decided the user should be told — a measure nobody explains is a broken VPN.
func (m *Manager) notifyAbuseUser(set *model.Settings, u model.User, html string) {
	if u.TgChatID == 0 || !set.TGUserBotEnabled {
		return
	}
	m.notifyUser(u.TgChatID, html)
}

// speedLabel words a cap the way a person reads it: whole megabits when it is
// one, kilobits otherwise.
func speedLabel(lang i18n.Lang, kbps int) string {
	if kbps >= 1000 && kbps%1000 == 0 {
		return i18n.T(lang, "notify.speedMbps", kbps/1000)
	}
	return i18n.T(lang, "notify.speedKbps", kbps)
}
