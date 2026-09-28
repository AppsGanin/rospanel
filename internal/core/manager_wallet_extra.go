package core

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Refunds to the balance, and what the panel shows about the referral programme and
// promo codes.

// RefundOrder returns a paid plan order's price to the user's balance — the money
// and the balance part alike — once. cancelPlan
// also takes back the days the order bought, when its plan is still the user's
// active one (a dispute: the money back and the access gone). Needs the wallet: the
// balance is where the money goes.
func (m *Manager) RefundOrder(ctx context.Context, orderID int64, cancelPlan bool) (int64, error) {
	set, err := m.Settings()
	if err != nil {
		return 0, err
	}
	if !set.WalletEnabled {
		return 0, invalidCode("err.walletOff", "баланс выключен")
	}
	order, err := m.store.GetPaymentOrder(orderID)
	if err != nil {
		return 0, err
	}
	userID, kop, err := m.store.RefundOrder(orderID, time.Now().Unix())
	if errors.Is(err, store.ErrNotRefundable) {
		return 0, invalidCode("err.notRefundable", "вернуть можно только оплаченный заказ на тариф, один раз")
	}
	if err != nil {
		return 0, err
	}
	u, err := m.store.GetUser(userID)
	if err != nil {
		return kop, nil
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventPaymentRefunded, map[string]any{
		"order_id": order.ID, "plan": order.PlanName, "refund_kop": kop, "plan_cancelled": cancelPlan,
	})
	if cancelPlan {
		if err := m.takeBackTerm(ctx, *u, order); err != nil {
			return kop, err
		}
	}
	if after, err := m.store.GetPaymentOrder(order.ID); err == nil {
		m.EmitWebhook(model.WebhookPaymentRefunded, after)
	}
	wal, _ := m.store.GetWalletLite(u.ID)
	m.notifyUserEvent(set, *u, model.UserNotifyPayment,
		i18n.T(m.userLang(u.TgChatID), "notify.userRefunded", kopText(kop), order.ID, kopText(wal.BalanceKop)))
	return kop, nil
}

// takeBackTerm undoes what a refunded order bought: its own periods come off the end
// of the term, and only a term that leaves nothing (or a lifetime plan) ends the plan.
// Whatever else paid for the term — other orders, bonus days — stays paid for. Not a
// lapse the user chose, so no win-back code follows it.
func (m *Manager) takeBackTerm(ctx context.Context, u model.User, order *model.PaymentOrder) error {
	active := m.ActivePaidPlan(u)
	if active == nil || active.ID != order.PlanID {
		return nil
	}
	if active.PeriodDays <= 0 {
		return m.cancelUserPlan(ctx, u.ID, false)
	}
	now := time.Now().Unix()
	secs := int64(active.PeriodDays) * int64(max(order.Periods, 1)) * 86400
	m.applyPlanMu.Lock()
	exp, err := m.store.ShortenTerm(u.ID, active.ID, secs, now)
	m.applyPlanMu.Unlock()
	if err != nil {
		return err
	}
	if exp > 0 && exp <= now {
		return m.cancelUserPlan(ctx, u.ID, false)
	}
	m.TriggerUserSync()
	return nil
}

// Referrals lists the newest limit users someone invited.
func (m *Manager) Referrals(userID int64, limit int) ([]model.Referral, error) {
	return m.store.ListReferrals(userID, limit)
}

// PromoUsage is who used a promo code and what money it brought.
type PromoUsage struct {
	Uses       []model.PromoUse `json:"uses"`
	Orders     int              `json:"orders"`      // paid orders it discounted
	RevenueRub int              `json:"revenue_rub"` // money those orders brought
}

// PromoUsage reads a code's uses and revenue.
func (m *Manager) PromoUsage(promoID int64) (*PromoUsage, error) {
	if _, err := m.store.GetPromo(promoID); errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	uses, err := m.store.ListPromoUses(promoID, 500)
	if err != nil {
		return nil, err
	}
	orders, rub, err := m.store.PromoRevenue(promoID)
	if err != nil {
		return nil, err
	}
	return &PromoUsage{Uses: uses, Orders: orders, RevenueRub: rub}, nil
}

// ReferralStats sums up the referral programme.
func (m *Manager) ReferralStats() (model.ReferralStats, error) {
	return m.store.ReferralStats(10)
}
