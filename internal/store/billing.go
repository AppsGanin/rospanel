package store

import (
	"database/sql"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// ListTariffPlans returns plans sorted for display.
func (s *Store) ListTariffPlans(includeDisabled bool) ([]model.TariffPlan, error) {
	q := `SELECT id, slug, name, price_rub, period_days, data_limit, device_limit,
	             sort_order, enabled
	      FROM tariff_plans`
	if !includeDisabled {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY sort_order ASC, id ASC`
	return s.scanPlans(q)
}

func (s *Store) GetTariffPlan(id int64) (*model.TariffPlan, error) {
	plans, err := s.scanPlans(`SELECT id, slug, name, price_rub, period_days, data_limit, device_limit,
		sort_order, enabled FROM tariff_plans WHERE id = ?`, id)
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
			 is_free, sort_order, enabled)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			p.Slug, p.Name, p.PriceRub, p.PeriodDays, p.DataLimit, p.DeviceLimit,
			boolToInt(p.IsFree()), p.SortOrder, boolToInt(p.Enabled),
		).Scan(&p.ID)
	}
	_, err := s.db.Exec(
		`UPDATE tariff_plans SET slug=?, name=?, price_rub=?, period_days=?, data_limit=?,
		 device_limit=?, is_free=?, sort_order=?, enabled=? WHERE id=?`,
		p.Slug, p.Name, p.PriceRub, p.PeriodDays, p.DataLimit, p.DeviceLimit,
		boolToInt(p.IsFree()), p.SortOrder, boolToInt(p.Enabled), p.ID,
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

// UserIDsOnPlan returns the ids of users currently assigned to a plan (used to
// migrate them when a plan is retired).
func (s *Store) UserIDsOnPlan(planID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM users WHERE plan_id = ?`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PaidByProvider returns paid-order revenue grouped by provider ("" = manual),
// highest-earning first. Queried against payment_orders directly (no user join) so
// revenue from since-deleted users is still counted.
func (s *Store) PaidByProvider() ([]model.ProviderStat, error) {
	rows, err := s.db.Query(`
		SELECT provider, count(*), COALESCE(sum(amount_rub), 0)
		FROM payment_orders WHERE status = 'paid'
		GROUP BY provider ORDER BY sum(amount_rub) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProviderStat
	for rows.Next() {
		var p model.ProviderStat
		if err := rows.Scan(&p.Provider, &p.Count, &p.Sum); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PaidSumSince returns the total paid revenue whose paid_at is at or after since.
func (s *Store) PaidSumSince(since int64) (int, error) {
	var v int
	err := s.db.QueryRow(
		`SELECT COALESCE(sum(amount_rub), 0) FROM payment_orders WHERE status = 'paid' AND paid_at >= ?`,
		since,
	).Scan(&v)
	return v, err
}

// PendingTotals returns the count and rouble total of orders awaiting payment.
func (s *Store) PendingTotals() (count, sum int, err error) {
	err = s.db.QueryRow(
		`SELECT count(*), COALESCE(sum(amount_rub), 0) FROM payment_orders WHERE status = 'pending'`,
	).Scan(&count, &sum)
	return count, sum, err
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
		var en int
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Name, &p.PriceRub, &p.PeriodDays, &p.DataLimit, &p.DeviceLimit,
			&p.SortOrder, &en,
		); err != nil {
			return nil, err
		}
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

// LatestPendingManualOrder returns the newest still-pending manual order (no
// provider set) for a user+plan, or sql.ErrNoRows. Lets callers reuse an order
// instead of piling up duplicates when the user re-taps "Pay".
func (s *Store) LatestPendingManualOrder(userID, planID int64) (*model.PaymentOrder, error) {
	orders, err := s.listPaymentOrders(
		`SELECT `+orderCols+`
		 FROM payment_orders o
		 JOIN users u ON u.id = o.user_id
		 JOIN tariff_plans p ON p.id = o.plan_id
		 WHERE o.user_id = ? AND o.plan_id = ? AND o.status = 'pending'
		   AND (o.provider IS NULL OR o.provider = '')
		 ORDER BY o.created_at DESC LIMIT 1`, userID, planID)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, sql.ErrNoRows
	}
	return &orders[0], nil
}

// LatestPendingProviderOrder returns the newest still-pending order that went
// through an automatic provider for a user (or sql.ErrNoRows). Used by the
// subscription page to show a "payment processing" state after the user returns
// from the provider until the webhook/poll confirms it.
func (s *Store) LatestPendingProviderOrder(userID int64) (*model.PaymentOrder, error) {
	orders, err := s.listPaymentOrders(
		`SELECT `+orderCols+`
		 FROM payment_orders o
		 JOIN users u ON u.id = o.user_id
		 JOIN tariff_plans p ON p.id = o.plan_id
		 WHERE o.user_id = ? AND o.status = 'pending' AND o.provider <> ''
		 ORDER BY o.created_at DESC LIMIT 1`, userID)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, sql.ErrNoRows
	}
	return &orders[0], nil
}

// LatestPendingProviderOrderForPlan returns the newest pending order for a
// user+plan+provider (or sql.ErrNoRows). Lets the pay flow reuse a fresh order
// instead of creating duplicates on repeated taps.
func (s *Store) LatestPendingProviderOrderForPlan(userID, planID int64, provider string) (*model.PaymentOrder, error) {
	orders, err := s.listPaymentOrders(
		`SELECT `+orderCols+`
		 FROM payment_orders o
		 JOIN users u ON u.id = o.user_id
		 JOIN tariff_plans p ON p.id = o.plan_id
		 WHERE o.user_id = ? AND o.plan_id = ? AND o.provider = ? AND o.status = 'pending'
		 ORDER BY o.created_at DESC LIMIT 1`, userID, planID, provider)
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

// CancelPaymentOrderIfPending cancels an order only while it's still pending, so a
// stale-order sweep or a provider "canceled" status can't clobber an order a
// concurrent webhook just marked paid.
func (s *Store) CancelPaymentOrderIfPending(id int64) error {
	_, err := s.db.Exec(
		`UPDATE payment_orders SET status = 'cancelled', paid_at = 0 WHERE id = ? AND status = 'pending'`, id)
	return err
}

// MarkPaymentOrderPaidIfPending atomically transitions an order pending→paid and
// reports whether THIS call performed the transition. Exactly one of several
// concurrent confirmers (provider webhook + the poll fallback + a re-delivered
// webhook) wins the CAS; a caller that gets false must not apply the plan, so a
// single payment can never extend the user twice.
func (s *Store) MarkPaymentOrderPaidIfPending(id, paidAt int64) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE payment_orders SET status = 'paid', paid_at = ? WHERE id = ? AND status = 'pending'`,
		paidAt, id,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RevertPaymentOrderToPending puts a claimed order back to pending so the polling
// fallback retries — used when applying the plan failed after the paid claim.
func (s *Store) RevertPaymentOrderToPending(id int64) error {
	_, err := s.db.Exec(`UPDATE payment_orders SET status = 'pending', paid_at = 0 WHERE id = ?`, id)
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
