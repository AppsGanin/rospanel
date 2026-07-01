package core

import (
	"fmt"
	"net/url"
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
	if p.IsFree {
		p.PriceRub = 0
		p.PeriodDays = 0
	} else if !p.IsFree && p.PriceRub <= 0 && p.PeriodDays > 0 {
		return invalid("укажите цену для платного тарифа")
	}
	if p.SortOrder < 0 {
		p.SortOrder = 0
	}
	p.PaymentURL = strings.TrimSpace(p.PaymentURL)
	if p.PaymentURL != "" {
		if err := validateHTTPSURL(p.PaymentURL); err != nil {
			return err
		}
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
			logInfo("user %d registered with trial plan %q (%d days)", u.ID, plan.Name, set.BillingTrialDays)
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
			if err := m.applyPlanLimits(u.ID, plan, 0, plan.IsFree); err != nil {
				return nil, err
			}
			_ = m.store.SetUserPlan(u.ID, plan.ID, false)
			logInfo("user %d registered with free plan %q", u.ID, plan.Name)
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

func (m *Manager) applyPlanLimits(userID int64, plan *model.TariffPlan, expireAt int64, monthlyReset bool) error {
	period := "none"
	if monthlyReset && plan.DataLimit > 0 {
		period = "monthly"
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
	now := time.Now().Unix()
	var expire int64
	if plan.IsFree {
		expire = 0
	} else if plan.PeriodDays > 0 {
		base := now
		if extendFromCurrent && u.ExpireAt > now {
			base = u.ExpireAt
		}
		expire = base + int64(plan.PeriodDays)*86400
	}
	if err := m.applyPlanLimits(userID, plan, expire, plan.IsFree); err != nil {
		return err
	}
	if err := m.store.SetUserPlan(userID, plan.ID, trial); err != nil {
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
			logErr("billing: downgrade user %d to free: %v", u.ID, err)
			continue
		}
		logInfo("billing: user %d downgraded to free plan after expiry", u.ID)
	}
	return nil
}

// RequestPlanPayment creates a pending order for a paid plan.
func (m *Manager) RequestPlanPayment(userID, planID int64) (*model.PaymentOrder, string, error) {
	plan, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return nil, "", err
	}
	if plan.IsFree || plan.PriceRub <= 0 {
		return nil, "", invalid("этот тариф бесплатный")
	}
	order, err := m.store.CreatePaymentOrder(userID, planID, plan.PriceRub)
	if err != nil {
		return nil, "", err
	}
	set, _ := m.Settings()
	msg := fmt.Sprintf("Заказ #%d\nТариф: %s\nСумма: %d ₽", order.ID, plan.Name, plan.PriceRub)
	if plan.PaymentURL != "" {
		msg += "\n\nСсылка для оплаты:\n" + plan.PaymentURL
	}
	if set != nil && strings.TrimSpace(set.BillingPaymentNote) != "" {
		msg += "\n\n" + strings.TrimSpace(set.BillingPaymentNote)
	}
	msg += fmt.Sprintf("\n\nВ комментарии к переводу укажите: заказ #%d", order.ID)
	m.notifyAdminEvent(model.AdminEventPayment, fmt.Sprintf(
		"🛒 <b>Новый заказ (ручная оплата)</b>\nЗаказ #%d · %s\nТариф: %s · %d ₽\nЖдёт подтверждения админом.",
		order.ID, escHTML(order.UserName), escHTML(plan.Name), plan.PriceRub))
	return order, msg, nil
}

// ConfirmPayment marks an order paid and applies the plan.
func (m *Manager) ConfirmPayment(orderID int64) error {
	order, err := m.store.GetPaymentOrder(orderID)
	if err != nil {
		return err
	}
	if order.Status != "pending" {
		return invalid("заказ уже обработан")
	}
	now := time.Now().Unix()
	if err := m.ApplyPlanToUser(order.UserID, order.PlanID, true); err != nil {
		return err
	}
	if err := m.store.SetPaymentOrderStatus(orderID, "paid", now); err != nil {
		return err
	}
	logInfo("billing: order %d confirmed, user %d plan %d", orderID, order.UserID, order.PlanID)
	return nil
}

func (m *Manager) CancelPayment(orderID int64) error {
	return m.store.SetPaymentOrderStatus(orderID, "cancelled", 0)
}

func (m *Manager) ListPaymentOrders(status string) ([]model.PaymentOrder, error) {
	return m.store.ListPaymentOrders(status, 100)
}

// PurchasablePlans lists paid plans a user can buy (excludes free, excludes current if not expired).
func (m *Manager) PurchasablePlans() ([]model.TariffPlan, error) {
	all, err := m.store.ListTariffPlans(false)
	if err != nil {
		return nil, err
	}
	var out []model.TariffPlan
	for _, p := range all {
		if !p.IsFree && p.PriceRub > 0 {
			out = append(out, p)
		}
	}
	return out, nil
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

func validateHTTPSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return invalid("ссылка оплаты должна быть https://…")
	}
	if u.User != nil {
		return invalid("учётные данные в ссылке оплаты не допускаются")
	}
	return nil
}
