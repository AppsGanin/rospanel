package core

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/google/uuid"
)

// ListTariffPlans returns tariff plans for admin UI.
func (m *Manager) ListTariffPlans(includeDisabled bool) ([]model.TariffPlan, error) {
	return m.store.ListTariffPlans(includeDisabled)
}

func (m *Manager) SaveTariffPlan(p *model.TariffPlan) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return invalid("укажите название тарифа")
	}
	p.Slug = strings.TrimSpace(p.Slug)
	if p.Slug == "" {
		p.Slug = slugifyPlan(p.Name)
	}
	if !slugRe.MatchString(p.Slug) {
		return invalid("код тарифа: только латинские буквы, цифры и дефис")
	}
	// Price defines the tier: 0 ⇒ free (never expires, quota refills every срок
	// действия via PeriodDays); > 0 ⇒ paid (expires after PeriodDays). There is no
	// separate "free" flag — see model.TariffPlan.IsFree.
	if p.PriceRub < 0 {
		p.PriceRub = 0
	}
	if p.SortOrder < 0 {
		p.SortOrder = 0
	}
	if err := m.store.SaveTariffPlan(p); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return invalid("тариф с таким кодом уже существует")
		}
		return err
	}
	return nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func slugifyPlan(name string) string {
	s := strings.ToLower(name)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "plan"
	}
	return out
}

func (m *Manager) DeleteTariffPlan(id int64) error {
	set, err := m.Settings()
	if err != nil {
		return err
	}
	if set.BillingFreePlanID == id || set.BillingTrialPlanID == id {
		return invalid("тариф указан в настройках биллинга — сначала выберите другой")
	}
	n, err := m.store.CountUsersOnPlan(id)
	if err != nil {
		return err
	}
	if n > 0 {
		return invalid("тариф назначен %d пользователям — сначала смените им тариф", n)
	}
	return m.store.DeleteTariffPlan(id)
}

// MigratePlanUsers moves every user currently on fromPlanID to toPlanID (applying
// the target plan's limits and period). Used when retiring a plan. Returns how many
// users were moved.
func (m *Manager) MigratePlanUsers(fromPlanID, toPlanID int64) (int, error) {
	if fromPlanID == toPlanID {
		return 0, invalid("выберите другой тариф для перевода")
	}
	if _, err := m.store.GetTariffPlan(toPlanID); err != nil {
		return 0, invalid("целевой тариф не найден")
	}
	ids, err := m.store.UserIDsOnPlan(fromPlanID)
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, id := range ids {
		if err := m.ApplyPlanToUser(id, toPlanID, false); err != nil {
			logErr("billing: plan migration failed", "user", id, "from_plan", fromPlanID, "to_plan", toPlanID, "err", err)
			continue
		}
		migrated++
	}
	logInfo("billing: migrated users between plans", "migrated", migrated, "total", len(ids), "from_plan", fromPlanID, "to_plan", toPlanID)
	return migrated, nil
}

func (m *Manager) SaveBillingSettings(st *model.Settings) error {
	if st.BillingTrialDays < 0 {
		return invalid("пробный период не может быть отрицательным")
	}
	return m.store.SetBillingSettings(st)
}

// CreateRegisteredUser creates a user from self-registration (trial/free/plain
// per billing config) and alerts the admin chats about the new signup.
func (m *Manager) CreateRegisteredUser(name string) (*model.User, error) {
	u, err := m.createRegisteredUser(name)
	if err == nil && u != nil {
		msg := "🆕 <b>Новая регистрация</b>\nПользователь: " + escHTML(u.Name)
		if plan := m.PlanName(u.PlanID); plan != "" {
			msg += "\nТариф: " + escHTML(plan)
		}
		m.notifyAdminEvent(model.AdminEventRegistered, msg)
		m.EmitWebhook(model.WebhookUserRegistered, userEventData(*u))
	}
	return u, err
}

// createRegisteredUser is the registration body: trial → free → plain user.
func (m *Manager) createRegisteredUser(name string) (*model.User, error) {
	// Self-registration name comes from the Telegram display name — bound its length
	// (truncate rather than reject) so it can't bloat the DB / config unboundedly.
	name = truncateName(name)
	if name == "" {
		return nil, invalid("укажите имя")
	}
	set, err := m.Settings()
	if err != nil {
		return nil, err
	}
	if !set.BillingEnabled {
		return m.CreateUser(name, 0, 0)
	}
	now := time.Now().Unix()
	if set.BillingTrialDays > 0 && set.BillingTrialPlanID > 0 {
		plan, err := m.store.GetTariffPlan(set.BillingTrialPlanID)
		if err == nil && plan != nil && plan.Enabled {
			u, err := m.createBareUser(name)
			if err != nil {
				return nil, err
			}
			expire := now + int64(set.BillingTrialDays)*86400
			if err := m.applyPlanLimits(u.ID, plan, expire, false); err != nil {
				return nil, err
			}
			_ = m.store.SetUserPlan(u.ID, plan.ID, true)
			logInfo("user registered with trial plan", "user", u.ID, "plan", plan.Name, "days", set.BillingTrialDays)
			m.TriggerUserSync()
			return m.store.GetUser(u.ID)
		}
	}
	if set.BillingFreePlanID > 0 {
		plan, err := m.store.GetTariffPlan(set.BillingFreePlanID)
		if err == nil && plan != nil {
			u, err := m.createBareUser(name)
			if err != nil {
				return nil, err
			}
			if err := m.applyPlanLimits(u.ID, plan, 0, plan.IsFree()); err != nil {
				return nil, err
			}
			_ = m.store.SetUserPlan(u.ID, plan.ID, false)
			logInfo("user registered with free plan", "user", u.ID, "plan", plan.Name)
			m.TriggerUserSync()
			return m.store.GetUser(u.ID)
		}
	}
	return m.CreateUser(name, 0, 0)
}

func (m *Manager) createBareUser(name string) (*model.User, error) {
	password, err := auth.RandomPassword()
	if err != nil {
		return nil, err
	}
	subToken, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	return m.store.CreateUser(name, uuid.NewString(), password, subToken, 0, 0, 0)
}

func (m *Manager) applyPlanLimits(userID int64, plan *model.TariffPlan, expireAt int64, freeReset bool) error {
	period := "none"
	if freeReset && plan.DataLimit > 0 && plan.PeriodDays > 0 {
		// Free plan: refill the quota every срок действия (rolling N-day cycle),
		// not on a fixed calendar month. A "бессрочно" free plan (PeriodDays 0)
		// never resets — its quota is one-time.
		period = fmt.Sprintf("days:%d", plan.PeriodDays)
	}
	if err := m.store.SetUserLimits(userID, plan.DataLimit, expireAt, plan.DeviceLimit); err != nil {
		return err
	}
	return m.store.SetResetPeriod(userID, period, time.Now().Unix())
}

// ApplyPlanToUser assigns a tariff and updates limits. extendFromCurrent stacks paid time.
// planID 0 switches to manual mode: clears plan link and resets limits to unlimited.
func (m *Manager) ApplyPlanToUser(userID int64, planID int64, extendFromCurrent bool) error {
	// Serialize the expire_at read-modify-write below against concurrent confirmers.
	m.applyPlanMu.Lock()
	defer m.applyPlanMu.Unlock()
	u, err := m.store.GetUser(userID)
	if err != nil {
		return err
	}
	trial := u.TrialUsed
	if planID == 0 {
		if err := m.store.SetUserLimits(userID, 0, 0, 0); err != nil {
			return err
		}
		if err := m.store.SetResetPeriod(userID, "none", time.Now().Unix()); err != nil {
			return err
		}
		if err := m.store.SetUserPlan(userID, 0, trial); err != nil {
			return err
		}
		m.TriggerUserSync()
		return nil
	}
	plan, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return err
	}
	set, err := m.Settings()
	if err != nil {
		return err
	}
	// The designated trial plan is a zero-price template that still EXPIRES when
	// assigned (period-limited proba), so it is NOT treated as a free plan here
	// even though its price is 0 — a manual assignment gives period_days of access,
	// then EnforceBilling downgrades it to the free plan, same as the trial flow.
	freePlan := plan.IsFree() && plan.ID != set.BillingTrialPlanID
	now := time.Now().Unix()
	var expire int64
	if freePlan {
		expire = 0
	} else if plan.PeriodDays > 0 {
		base := now
		if extendFromCurrent && u.ExpireAt > now {
			base = u.ExpireAt
		}
		expire = base + int64(plan.PeriodDays)*86400
	}
	if err := m.applyPlanLimits(userID, plan, expire, freePlan); err != nil {
		return err
	}
	if err := m.store.SetUserPlan(userID, plan.ID, trial); err != nil {
		return err
	}
	m.TriggerUserSync()
	return nil
}

// isPlanRenewal reports whether applying planID to the user is a renewal of their
// currently-active paid plan — the only case where paid time should extend from the
// current expiry instead of starting from now (buying from trial/free/expired must
// start fresh, not inherit the remaining time).
func (m *Manager) isPlanRenewal(userID, planID int64) bool {
	u, err := m.store.GetUser(userID)
	if err != nil {
		return false
	}
	ap := m.ActivePaidPlan(*u)
	return ap != nil && ap.ID == planID
}

// ActivePaidPlan returns the user's current tariff when it's a paid plan that is
// still active (expiry in the future), else nil. This is the "locked" state where
// only renewal or cancellation is allowed — not switching to another plan. A trial
// or free plan (price 0) never counts, so upgrading from those stays open.
func (m *Manager) ActivePaidPlan(u model.User) *model.TariffPlan {
	if u.PlanID == 0 || u.ExpireAt <= time.Now().Unix() {
		return nil
	}
	plan, err := m.store.GetTariffPlan(u.PlanID)
	if err != nil || plan == nil || plan.IsFree() {
		return nil
	}
	return plan
}

// CancelUserPlan cancels a paid subscription immediately: the user is moved to the
// free plan right away (losing any remaining paid time), matching what EnforceBilling
// does on expiry — but on demand. With no free plan configured, access is ended
// instead (plan cleared, expired now). The consumed-trial flag is preserved so
// cancelling can't reopen a fresh trial.
func (m *Manager) CancelUserPlan(userID int64) error {
	set, err := m.Settings()
	if err != nil {
		return err
	}
	if set.BillingFreePlanID != 0 {
		if free, err := m.store.GetTariffPlan(set.BillingFreePlanID); err == nil && free != nil {
			return m.ApplyPlanToUser(userID, free.ID, false)
		}
	}
	// No free plan: end the subscription now — clear the plan and expire immediately.
	u, err := m.store.GetUser(userID)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	if err := m.store.SetUserLimits(userID, u.DataLimit, now, u.DeviceLimit); err != nil {
		return err
	}
	if err := m.store.SetResetPeriod(userID, "none", now); err != nil {
		return err
	}
	if err := m.store.SetUserPlan(userID, 0, u.TrialUsed); err != nil {
		return err
	}
	m.TriggerUserSync()
	return nil
}

// EnforceBilling downgrades users whose paid/trial period ended to the free plan.
func (m *Manager) EnforceBilling(now int64) error {
	set, err := m.Settings()
	if err != nil || !set.BillingEnabled || set.BillingFreePlanID == 0 {
		return nil
	}
	free, err := m.store.GetTariffPlan(set.BillingFreePlanID)
	if err != nil {
		return nil
	}
	users, err := m.store.UsersWithExpiredPlan(now)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.PlanID == free.ID {
			continue
		}
		if err := m.ApplyPlanToUser(u.ID, free.ID, false); err != nil {
			logErr("billing: downgrade to free failed", "user", u.ID, "err", err)
			continue
		}
		logInfo("billing: user downgraded to free plan after expiry", "user", u.ID)
	}
	return nil
}

// RequestPlanPayment opens a pending manual order for a paid plan and returns the
// payment instructions. To keep a spammed "Pay" button from piling up duplicate
// orders (and admin pings), it reuses the user's latest still-pending manual order
// for the same plan instead of creating another.
func (m *Manager) RequestPlanPayment(userID, planID int64) (*model.PaymentOrder, string, error) {
	plan, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return nil, "", invalid("тариф не найден")
	}
	if plan.IsFree() {
		return nil, "", invalid("этот тариф бесплатный")
	}
	// Same rules as the automatic path: block switching (and buying a disabled plan)
	// while a paid one is active — but let an existing subscriber renew the plan
	// they're already on, even if it's since been disabled (grandfathering).
	if u, err := m.store.GetUser(userID); err == nil && u.PlanID != planID {
		if !plan.Enabled {
			return nil, "", invalid("тариф недоступен")
		}
		if cur := m.ActivePaidPlan(*u); cur != nil {
			return nil, "", invalid("у вас активна подписка «%s» — сначала отмените её, чтобы сменить тариф", cur.Name)
		}
	}
	set, _ := m.Settings()
	if existing, err := m.store.LatestPendingManualOrder(userID, planID); err == nil && existing != nil {
		return existing, manualOrderMessage(existing, plan, set), nil // reuse, no new order/notification
	}
	order, err := m.store.CreatePaymentOrder(userID, planID, plan.PriceRub)
	if err != nil {
		return nil, "", err
	}
	m.notifyAdminEvent(model.AdminEventPayment, fmt.Sprintf(
		"🛒 <b>Новый заказ (ручная оплата)</b>\nЗаказ #%d · %s\nТариф: %s · %d ₽\nЖдёт подтверждения админом.",
		order.ID, escHTML(order.UserName), escHTML(plan.Name), plan.PriceRub))
	m.EmitWebhook(model.WebhookPaymentCreated, order)
	return order, manualOrderMessage(order, plan, set), nil
}

// manualOrderMessage builds the user-facing manual-payment instructions for an
// order: amount, the operator's payment note, and the order number to quote in the
// transfer comment.
func manualOrderMessage(order *model.PaymentOrder, plan *model.TariffPlan, set *model.Settings) string {
	msg := fmt.Sprintf("Заказ #%d\nТариф: %s\nСумма: %d ₽", order.ID, plan.Name, plan.PriceRub)
	if set != nil && strings.TrimSpace(set.BillingPaymentNote) != "" {
		msg += "\n\n" + strings.TrimSpace(set.BillingPaymentNote)
	}
	msg += fmt.Sprintf("\n\nВ комментарии к переводу укажите: заказ #%d", order.ID)
	msg += "\n\nПосле подтверждения платежа администратором услуга будет активирована."
	return msg
}

// ConfirmPayment marks an order paid and applies the plan. Idempotent: the atomic
// pending→paid claim means a double-submit / retry (or an overlap with the provider
// webhook on a provider order) applies the plan at most once.
func (m *Manager) ConfirmPayment(orderID int64) error {
	order, err := m.store.GetPaymentOrder(orderID)
	if err != nil {
		return err
	}
	if order.Status != "pending" {
		return invalid("заказ уже обработан")
	}
	now := time.Now().Unix()
	claimed, err := m.store.MarkPaymentOrderPaidIfPending(orderID, now)
	if err != nil {
		return err
	}
	if !claimed {
		return invalid("заказ уже обработан")
	}
	// Extend from the current expiry only when this is a renewal of the active paid
	// plan; otherwise start from now.
	if err := m.ApplyPlanToUser(order.UserID, order.PlanID, m.isPlanRenewal(order.UserID, order.PlanID)); err != nil {
		_ = m.store.RevertPaymentOrderToPending(orderID) // let a retry re-apply
		return err
	}
	logInfo("billing: order confirmed", "order", orderID, "user", order.UserID, "plan", order.PlanID)
	order.Status, order.PaidAt = "paid", now
	m.EmitWebhook(model.WebhookPaymentPaid, order)
	return nil
}

func (m *Manager) CancelPayment(orderID int64) error {
	if err := m.store.SetPaymentOrderStatus(orderID, "cancelled", 0); err != nil {
		return err
	}
	// Best-effort payload enrichment: re-read the (now cancelled) order.
	if order, err := m.store.GetPaymentOrder(orderID); err == nil {
		m.EmitWebhook(model.WebhookPaymentCancelled, order)
	} else {
		m.EmitWebhook(model.WebhookPaymentCancelled, map[string]any{"id": orderID})
	}
	return nil
}

func (m *Manager) ListPaymentOrders(status string) ([]model.PaymentOrder, error) {
	return m.store.ListPaymentOrders(status, 100)
}

// PaymentStats assembles the revenue dashboard: all-time and per-provider paid
// totals, revenue since local midnight / the 1st of the month, and the pending
// backlog. Day/month boundaries use the operator's configured timezone.
func (m *Manager) PaymentStats() (*model.PaymentStats, error) {
	byProvider, err := m.store.PaidByProvider()
	if err != nil {
		return nil, err
	}
	var total, count int
	for _, p := range byProvider {
		total += p.Sum
		count += p.Count
	}
	now := time.Now().In(m.loc())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, m.loc()).Unix()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, m.loc()).Unix()
	today, err := m.store.PaidSumSince(dayStart)
	if err != nil {
		return nil, err
	}
	month, err := m.store.PaidSumSince(monthStart)
	if err != nil {
		return nil, err
	}
	pendingCount, pendingSum, err := m.store.PendingTotals()
	if err != nil {
		return nil, err
	}
	return &model.PaymentStats{
		TotalPaid:    total,
		PaidCount:    count,
		EarnedToday:  today,
		EarnedMonth:  month,
		PendingCount: pendingCount,
		PendingSum:   pendingSum,
		ByProvider:   byProvider,
	}, nil
}

func (m *Manager) PlanName(planID int64) string {
	if planID == 0 {
		return ""
	}
	p, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return ""
	}
	return p.Name
}
