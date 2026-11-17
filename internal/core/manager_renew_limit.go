package core

import (
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Renewal limits. Two operator settings, each with a per-plan override, keep users
// from buying a plan far ahead at today's price:
//
//   - the renewal window: an active plan can be renewed only in the last N days of
//     its term;
//   - the prepaid cap: a purchase may leave at most N days of paid time, counted from
//     now. One period always fits, so a plan with a cap below its period can be
//     renewed only once its term has run out.
//
// Both bind the user's own purchases — the subscription page, the bot, /v1 orders.
// The operator assigning a plan, and auto-renewal (one period, an hour before the
// end), are not held to them.

// renewLimits is the window and the cap in force for the plan, in days (0 = none).
func renewLimits(set *model.Settings, plan *model.TariffPlan) (window, maxDays int) {
	if set != nil {
		window, maxDays = set.RenewWindowDays, set.MaxPrepaidDays
	}
	if plan.RenewWindowDays > 0 {
		window = plan.RenewWindowDays
	}
	if plan.MaxPrepaidDays > 0 {
		maxDays = plan.MaxPrepaidDays
	}
	return window, maxDays
}

// prepaidLeft is the paid time the purchase of plan would add to: what is left of the
// term when it renews the active plan, else nothing (a purchase starts from now).
func (m *Manager) prepaidLeft(u model.User, plan *model.TariffPlan, now int64) int64 {
	if plan.PeriodDays <= 0 || !m.isPlanRenewalFor(u, plan.ID) {
		return 0
	}
	return max(u.ExpireAt-now, 0)
}

// RenewOpensAt is when the user may next renew plan, or 0 when they may now (or the
// limits do not apply: a plan without a term, or not a renewal).
func (m *Manager) RenewOpensAt(set *model.Settings, u model.User, plan *model.TariffPlan, now int64) int64 {
	left := m.prepaidLeft(u, plan, now)
	if left == 0 {
		return 0
	}
	window, maxDays := renewLimits(set, plan)
	period := int64(plan.PeriodDays) * 86400
	// How much may be left of the term for one more period to be bought.
	allowed := int64(-1)
	if window > 0 {
		allowed = int64(window) * 86400
	}
	if maxDays > 0 {
		capLeft := max(int64(maxDays)*86400, period) - period
		if allowed < 0 || capLeft < allowed {
			allowed = capLeft
		}
	}
	if allowed < 0 || left <= allowed {
		return 0
	}
	return u.ExpireAt - allowed
}

// periodsFit reports whether buying periods of plan stays within the prepaid cap.
func (m *Manager) periodsFit(set *model.Settings, u model.User, plan *model.TariffPlan, periods int, now int64) bool {
	_, maxDays := renewLimits(set, plan)
	if maxDays <= 0 || plan.PeriodDays <= 0 || periods <= 1 {
		return true
	}
	period := int64(plan.PeriodDays) * 86400
	return m.prepaidLeft(u, plan, now)+int64(periods)*period <= max(int64(maxDays)*86400, period)
}

// checkRenewLimits refuses a purchase the renewal window or the prepaid cap rules out.
func (m *Manager) checkRenewLimits(u model.User, plan *model.TariffPlan, periods int) error {
	set, err := m.Settings()
	if err != nil {
		return err
	}
	return m.checkRenewLimitsAt(set, u, plan, periods, time.Now().Unix())
}

func (m *Manager) checkRenewLimitsAt(set *model.Settings, u model.User, plan *model.TariffPlan, periods int, now int64) error {
	if at := m.RenewOpensAt(set, u, plan, now); at > 0 {
		return invalidCode("err.renewNotYet", "продлить можно с {{date}}",
			map[string]any{"date": RenewOpensText(at, m.loc())})
	}
	if !m.periodsFit(set, u, plan, max(periods, 1), now) {
		_, maxDays := renewLimits(set, plan)
		return invalidCode("err.prepaidTooLong", "оплатить можно не больше {{days}} дней вперёд",
			map[string]any{"days": max(maxDays, plan.PeriodDays)})
	}
	return nil
}

// RenewOpensText is the moment a renewal opens as users are shown it: the date and
// the time, since it is rarely midnight.
func RenewOpensText(at int64, loc *time.Location) string {
	return time.Unix(at, 0).In(loc).Format("02.01.2006 15:04")
}
