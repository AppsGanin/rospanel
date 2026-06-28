package store

import (
	"database/sql"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// ListTariffPlans returns plans sorted for display.
func (s *Store) ListTariffPlans(includeDisabled bool) ([]model.TariffPlan, error) {
	q := `SELECT id, slug, name, price_rub, period_days, data_limit, device_limit,
	             is_free, payment_url, sort_order, enabled
	      FROM tariff_plans`
	if !includeDisabled {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY sort_order ASC, id ASC`
	return s.scanPlans(q)
}

func (s *Store) GetTariffPlan(id int64) (*model.TariffPlan, error) {
	plans, err := s.scanPlans(`SELECT id, slug, name, price_rub, period_days, data_limit, device_limit,
		is_free, payment_url, sort_order, enabled FROM tariff_plans WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(plans) == 0 {
		return nil, sql.ErrNoRows
	}
	return &plans[0], nil
}

func (s *Store) SaveTariffPlan(p *model.TariffPlan) error {
	if p.ID == 0 {
		return s.db.QueryRow(
			`INSERT INTO tariff_plans (slug, name, price_rub, period_days, data_limit, device_limit,
			 is_free, payment_url, sort_order, enabled)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			p.Slug, p.Name, p.PriceRub, p.PeriodDays, p.DataLimit, p.DeviceLimit,
			boolToInt(p.IsFree), p.PaymentURL, p.SortOrder, boolToInt(p.Enabled),
		).Scan(&p.ID)
	}
	_, err := s.db.Exec(
		`UPDATE tariff_plans SET slug=?, name=?, price_rub=?, period_days=?, data_limit=?,
		 device_limit=?, is_free=?, payment_url=?, sort_order=?, enabled=? WHERE id=?`,
		p.Slug, p.Name, p.PriceRub, p.PeriodDays, p.DataLimit, p.DeviceLimit,
		boolToInt(p.IsFree), p.PaymentURL, p.SortOrder, boolToInt(p.Enabled), p.ID,
	)
	return err
}

func (s *Store) DeleteTariffPlan(id int64) error {
	_, err := s.db.Exec(`DELETE FROM tariff_plans WHERE id = ?`, id)
	return err
}

// CountUsersOnPlan returns how many users currently have this plan assigned.
func (s *Store) CountUsersOnPlan(planID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM users WHERE plan_id = ?`, planID).Scan(&n)
	return n, err
}

func (s *Store) scanPlans(query string, args ...any) ([]model.TariffPlan, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TariffPlan
	for rows.Next() {
		var p model.TariffPlan
		var free, en int
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Name, &p.PriceRub, &p.PeriodDays, &p.DataLimit, &p.DeviceLimit,
			&free, &p.PaymentURL, &p.SortOrder, &en,
		); err != nil {
			return nil, err
		}
		p.IsFree = free != 0
		p.Enabled = en != 0
		out = append(out, p)
	}
	if out == nil {
		out = []model.TariffPlan{}
	}
	return out, rows.Err()
}

func (s *Store) SetUserPlan(userID, planID int64, trialUsed bool) error {
	_, err := s.db.Exec(
		`UPDATE users SET plan_id = ?, trial_used = ? WHERE id = ?`,
		planID, boolToInt(trialUsed), userID,
	)
	return err
}

func (s *Store) UsersWithExpiredPlan(now int64) ([]model.User, error) {
	return s.queryUsers(
		`SELECT `+userCols+` FROM users
		 WHERE plan_id <> 0 AND expire_at > 0 AND expire_at <= ?`, now)
}

func (s *Store) CreatePaymentOrder(userID, planID int64, amountRub int) (*model.PaymentOrder, error) {
	now := time.Now().Unix()
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO payment_orders (user_id, plan_id, amount_rub, status, created_at)
		 VALUES (?, ?, ?, 'pending', ?) RETURNING id`,
		userID, planID, amountRub, now,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetPaymentOrder(id)
}

const orderCols = `o.id, o.user_id, u.name, o.plan_id, p.name, o.amount_rub, o.status,
	o.provider, o.provider_id, o.pay_url, o.created_at, o.paid_at`

func (s *Store) GetPaymentOrder(id int64) (*model.PaymentOrder, error) {
	orders, err := s.listPaymentOrders(
		`SELECT `+orderCols+`
		 FROM payment_orders o
		 JOIN users u ON u.id = o.user_id
		 JOIN tariff_plans p ON p.id = o.plan_id
		 WHERE o.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, sql.ErrNoRows
	}
	return &orders[0], nil
}

// GetPaymentOrderByProvider finds a pending-or-any order by its external id.
func (s *Store) GetPaymentOrderByProvider(provider, providerID string) (*model.PaymentOrder, error) {
	orders, err := s.listPaymentOrders(
		`SELECT `+orderCols+`
		 FROM payment_orders o
		 JOIN users u ON u.id = o.user_id
		 JOIN tariff_plans p ON p.id = o.plan_id
		 WHERE o.provider = ? AND o.provider_id = ?`, provider, providerID)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, sql.ErrNoRows
	}
	return &orders[0], nil
}

func (s *Store) ListPaymentOrders(status string, limit int) ([]model.PaymentOrder, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT ` + orderCols + `
	      FROM payment_orders o
	      JOIN users u ON u.id = o.user_id
	      JOIN tariff_plans p ON p.id = o.plan_id`
	args := []any{}
	if status != "" {
		q += ` WHERE o.status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY o.created_at DESC LIMIT ?`
	args = append(args, limit)
	return s.listPaymentOrders(q, args...)
}

func (s *Store) SetPaymentOrderStatus(id int64, status string, paidAt int64) error {
	_, err := s.db.Exec(`UPDATE payment_orders SET status = ?, paid_at = ? WHERE id = ?`, status, paidAt, id)
	return err
}

// SetPaymentOrderProvider links an order to an external provider payment.
func (s *Store) SetPaymentOrderProvider(id int64, provider, providerID, payURL string) error {
	_, err := s.db.Exec(
		`UPDATE payment_orders SET provider = ?, provider_id = ?, pay_url = ? WHERE id = ?`,
		provider, providerID, payURL, id)
	return err
}

func (s *Store) listPaymentOrders(query string, args ...any) ([]model.PaymentOrder, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PaymentOrder
	for rows.Next() {
		var o model.PaymentOrder
		if err := rows.Scan(
			&o.ID, &o.UserID, &o.UserName, &o.PlanID, &o.PlanName,
			&o.AmountRub, &o.Status, &o.Provider, &o.ProviderID, &o.PayURL,
			&o.CreatedAt, &o.PaidAt,
		); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) SetBillingSettings(st *model.Settings) error {
	_, err := s.db.Exec(
		`UPDATE settings SET billing_enabled = ?, billing_trial_days = ?,
		 billing_free_plan_id = ?, billing_trial_plan_id = ?, billing_payment_note = ?,
		 updated_at = unixepoch() WHERE id = 1`,
		boolToInt(st.BillingEnabled), st.BillingTrialDays,
		st.BillingFreePlanID, st.BillingTrialPlanID, st.BillingPaymentNote,
	)
	return err
}

// SetPaymentSettings persists the payment-provider config (secrets encrypted).
func (s *Store) SetPaymentSettings(st *model.Settings) error {
	_, err := s.db.Exec(
		`UPDATE settings SET yookassa_enabled = ?, yookassa_shop_id = ?, yookassa_secret_key = ?,
		 yookassa_test = ?, cryptobot_enabled = ?, cryptobot_token = ?, cryptobot_testnet = ?,
		 updated_at = unixepoch() WHERE id = 1`,
		boolToInt(st.YooKassaEnabled), st.YooKassaShopID, encField(st.YooKassaSecretKey),
		boolToInt(st.YooKassaTest), boolToInt(st.CryptoBotEnabled), encField(st.CryptoBotToken),
		boolToInt(st.CryptoBotTestnet),
	)
	return err
}

// SetPaymentWebhookSecret stores the random webhook URL segment.
func (s *Store) SetPaymentWebhookSecret(secret string) error {
	return s.setSetting("payment_webhook_secret", secret)
}

// PendingProviderOrders returns pending orders that were started through a payment
// provider (for the polling fallback). Stale ones (older than maxAge seconds) are
// skipped — the caller marks them cancelled.
func (s *Store) PendingProviderOrders(limit int) ([]model.PaymentOrder, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.listPaymentOrders(
		`SELECT `+orderCols+`
		 FROM payment_orders o
		 JOIN users u ON u.id = o.user_id
		 JOIN tariff_plans p ON p.id = o.plan_id
		 WHERE o.status = 'pending' AND o.provider != '' AND o.provider_id != ''
		 ORDER BY o.created_at ASC LIMIT ?`, limit)
}
