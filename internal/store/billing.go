package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// planSpeedSet writes a tariff's speed cap onto a user row (two ? placeholders, both
// the cap). While a blocklist throttle is in force the row's speed_limit holds the
// throttle and abuse_prev_speed the cap its lift will put back — so the tariff's cap
// goes to the second. Written to the first, it would lift the throttle early and be
// replaced by a stale cap when the throttle ran out.
const planSpeedSet = `speed_limit = CASE WHEN abuse_action = '` + model.AbuseActionThrottle + `' THEN speed_limit ELSE ? END,
	abuse_prev_speed = CASE WHEN abuse_action = '` + model.AbuseActionThrottle + `' THEN ? ELSE abuse_prev_speed END`

// SetPlanUsersSpeedLimit stamps a plan's speed cap onto every user currently on it,
// returning how many rows changed. Called when the plan's cap is edited — see
// Manager.SaveTariffPlan for why this one limit is retroactive and the others aren't.
func (s *Store) SetPlanUsersSpeedLimit(planID int64, kbps int) (int64, error) {
	res, err := s.db.Exec(
		`UPDATE users SET `+planSpeedSet+`
		 WHERE plan_id = ? AND (CASE WHEN abuse_action = '`+model.AbuseActionThrottle+`' THEN abuse_prev_speed ELSE speed_limit END) != ?`,
		kbps, kbps, planID, kbps)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// SetPlanUsersResetPeriod stamps a plan's quota-reset cycle onto every user currently
// on it, anchoring the cycle at now for the rows it changes, and returns how many
// changed. The cycle is a policy value like the speed cap — it moves no date and
// zeroes no counter — so it is retroactive for the same reason (Manager.SaveTariffPlan).
func (s *Store) SetPlanUsersResetPeriod(planID int64, period string, now int64) (int64, error) {
	res, err := s.db.Exec(
		`UPDATE users SET reset_period = ?, last_reset_at = ?
		  WHERE plan_id = ? AND reset_period != ?`,
		period, now, planID, period)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListTariffPlans returns plans sorted for display.
func (s *Store) ListTariffPlans(includeDisabled bool) ([]model.TariffPlan, error) {
	q := `SELECT id, slug, name, price_rub, period_days, data_limit, device_limit,
	             speed_limit, reset_period, sort_order, enabled, device_price, device_max
	      FROM tariff_plans`
	if !includeDisabled {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY sort_order ASC, id ASC`
	return s.scanPlans(q)
}

func (s *Store) GetTariffPlan(id int64) (*model.TariffPlan, error) {
	plans, err := s.scanPlans(`SELECT id, slug, name, price_rub, period_days, data_limit, device_limit,
		speed_limit, reset_period, sort_order, enabled, device_price, device_max FROM tariff_plans WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(plans) == 0 {
		return nil, sql.ErrNoRows
	}
	return &plans[0], nil
}

// SaveTariffPlan writes the plan row and the access groups it grants together — the
// groups are part of what the plan IS, so a half-saved plan (new limits, old groups)
// would be applied to buyers until someone noticed.
func (s *Store) SaveTariffPlan(p *model.TariffPlan) error {
	// A rolled-back INSERT must not leave its id on the caller's struct: the plan row
	// is gone, so a retry with that id would UPDATE nothing and report success, and the
	// operator's plan would quietly not exist.
	created := p.ID == 0
	err := s.withTx(func(tx *sql.Tx) error {
		if p.ID == 0 {
			if err := tx.QueryRow(
				`INSERT INTO tariff_plans (slug, name, price_rub, period_days, data_limit, device_limit,
				 speed_limit, reset_period, is_free, sort_order, enabled, device_price, device_max)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
				p.Slug, p.Name, p.PriceRub, p.PeriodDays, p.DataLimit, p.DeviceLimit,
				p.SpeedLimit, p.ResetPeriod, boolToInt(p.IsFree()), p.SortOrder, boolToInt(p.Enabled),
				p.DevicePrice, p.DeviceMax,
			).Scan(&p.ID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(
			`UPDATE tariff_plans SET slug=?, name=?, price_rub=?, period_days=?, data_limit=?,
			 device_limit=?, speed_limit=?, reset_period=?, is_free=?, sort_order=?, enabled=?,
			 device_price=?, device_max=? WHERE id=?`,
			p.Slug, p.Name, p.PriceRub, p.PeriodDays, p.DataLimit, p.DeviceLimit,
			p.SpeedLimit, p.ResetPeriod, boolToInt(p.IsFree()), p.SortOrder, boolToInt(p.Enabled),
			p.DevicePrice, p.DeviceMax, p.ID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM plan_groups WHERE plan_id = ?`, p.ID); err != nil {
			return err
		}
		seen := map[int64]bool{}
		for _, gid := range p.GroupIDs {
			if gid == 0 || seen[gid] {
				continue
			}
			seen[gid] = true
			if _, err := tx.Exec(
				`INSERT INTO plan_groups (plan_id, group_id) VALUES (?, ?)`, p.ID, gid,
			); err != nil {
				return err
			}
		}
		// Everyone already on the plan moves with it: an operator who adds a group to a
		// plan means "this tariff includes that connection", not "from the next payment
		// on". Same transaction, so the plan and its members never disagree.
		return syncPlanMembersOn(tx, p.ID)
	})
	if err != nil && created {
		p.ID = 0
	}
	return err
}

// syncPlanMembersOn rewrites the plan-granted memberships of everyone currently on the
// plan to match what the plan now grants. Manual memberships (via_plan = 0) are left
// exactly as they are — see the migration.
func syncPlanMembersOn(ex execer, planID int64) error {
	if _, err := ex.Exec(`
		DELETE FROM group_members
		 WHERE via_plan = 1
		   AND user_id IN (SELECT id FROM users WHERE plan_id = ?)
		   AND group_id NOT IN (SELECT group_id FROM plan_groups WHERE plan_id = ?)`,
		planID, planID); err != nil {
		return err
	}
	_, err := ex.Exec(`
		INSERT INTO group_members (group_id, user_id, via_plan)
		SELECT pg.group_id, u.id, 1
		  FROM plan_groups pg JOIN users u ON u.plan_id = pg.plan_id
		 WHERE pg.plan_id = ?
		    ON CONFLICT (group_id, user_id) DO NOTHING`, planID)
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

// PurgeCancelledOrders drops cancelled/abandoned orders past the retention window.
//
// Deliberately NOT paid ones: those are the financial record and are kept. What grows
// without bound is the unpaid tail — every "pay" press in the public bot mints an order,
// and the 24h sweep cancels the ones that were never completed. Batched like every other
// sweep, because the writer is a single connection.
func (s *Store) PurgeCancelledOrders(before int64) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(
			`DELETE FROM payment_orders WHERE id IN (
				SELECT id FROM payment_orders
				WHERE status = 'cancelled' AND created_at < ? LIMIT ?
			)`, before, purgeBatch)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeBatch {
			return total, nil
		}
	}
}

// CountPendingOrdersForPlan returns how many orders are still awaiting payment for a
// plan. Deleting a plan out from under a pending order leaves the payment nothing to
// grant: the money can still be captured at the provider, and the confirm path then
// fails on the missing plan with the order left pending.
func (s *Store) CountPendingOrdersForPlan(planID int64) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM payment_orders WHERE plan_id = ? AND status = 'pending'`,
		planID).Scan(&n)
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
		FROM payment_orders WHERE status = 'paid' AND provider <> 'balance' AND refund_source <> 'provider'
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
		`SELECT COALESCE(sum(amount_rub), 0) FROM payment_orders
		 WHERE status = 'paid' AND paid_at >= ? AND refund_source <> 'provider'`,
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
	rows, err := s.rdb.Query(query, args...)
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
			&p.SpeedLimit, &p.ResetPeriod, &p.SortOrder, &en, &p.DevicePrice, &p.DeviceMax,
		); err != nil {
			return nil, err
		}
		p.Enabled = en != 0
		out = append(out, p)
	}
	if out == nil {
		out = []model.TariffPlan{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Grants in one pass over the (tiny) join table rather than a query per plan, the
	// same shape Groups() uses for its tokens.
	byID := make(map[int64]int, len(out))
	for i := range out {
		byID[out[i].ID] = i
	}
	grows, err := s.rdb.Query(`SELECT plan_id, group_id FROM plan_groups ORDER BY group_id`)
	if err != nil {
		return nil, err
	}
	defer grows.Close()
	for grows.Next() {
		var planID, groupID int64
		if err := grows.Scan(&planID, &groupID); err != nil {
			return nil, err
		}
		if i, ok := byID[planID]; ok {
			out[i].GroupIDs = append(out[i].GroupIDs, groupID)
		}
	}
	return out, grows.Err()
}

func (s *Store) SetUserPlan(userID, planID int64, trialUsed bool) error {
	return setUserPlanOn(s.db, userID, planID, trialUsed)
}

func setUserPlanOn(ex execer, userID, planID int64, trialUsed bool) error {
	_, err := ex.Exec(
		`UPDATE users SET plan_id = ?, trial_used = ? WHERE id = ?`,
		planID, boolToInt(trialUsed), userID,
	)
	return err
}

// UserPlanWrite is every users column a plan assignment touches. It exists so a
// caller can compute the whole target state up front and hand it over in one piece,
// because the three UPDATEs behind it have to land together: a user left with the
// new limits but the old plan_id (or vice versa) is a state nothing reconciles.
type UserPlanWrite struct {
	UserID      int64
	DataLimit   int64
	ExpireAt    int64
	DeviceLimit int
	SpeedLimit  int // kbit/s cap the plan promises (0 = unlimited)
	ResetPeriod string
	ResetAnchor int64 // last_reset_at: when the rolling quota cycle starts counting
	PlanID      int64
	TrialUsed   bool
	// GroupIDs are the access groups the new plan grants — the user's plan-granted
	// membership is replaced by exactly this set. It rides along here, rather than
	// being a separate call after the plan lands, for the reason the rest of this
	// struct exists: on the purchase path the plan is granted inside the order's paid
	// claim, and a group write left outside it could be lost to a crash with the money
	// already taken and nothing left pending to retry.
	GroupIDs []int64

	// ResetUsage zeroes the traffic counters as part of the same write: a new plan
	// brings a new quota, and carrying the old usage into it starts the cycle already
	// spent — which is how a user downgraded to a 1 GB free plan after burning 20 GB
	// found themselves cut off until the next refill, thirty days later.
	//
	// LastUp/LastDown re-baseline the raw counters to what Xray is reporting for this
	// user RIGHT NOW. Leaving them at zero is the trap store.ResetTraffic documents:
	// the next stats poll reads the cumulative Xray total, subtracts a baseline of 0,
	// and adds the user's whole lifetime traffic straight back.
	ResetUsage       bool
	LastUp, LastDown int64

	// ExtraDevices is how many devices beyond the plan's the user holds (already in
	// DeviceLimit); PackData the bought traffic already in DataLimit.
	ExtraDevices int
	PackData     int64
	// RequirePlan / RequireExpire, when set, make the write apply only while the user
	// is still on that plan with that term — a plan change priced for one term must
	// not land on another. ErrPlanStale otherwise.
	RequirePlan   int64
	RequireExpire int64
}

// ErrPlanStale is a plan write or an add-on whose user moved on since it was priced.
var ErrPlanStale = errors.New("the subscription changed since the price was computed")

// AddonWrite adds devices or traffic to the plan a user holds. RequireExpire, when
// set, holds it to the term it was priced for.
type AddonWrite struct {
	UserID        int64
	PlanID        int64
	AddDevices    int
	AddData       int64
	RequireExpire int64
	// MaxDevices caps the extra devices held after the add-on (the plan's DeviceMax).
	MaxDevices int
	// RequireExtra, when not negative, holds the add-on to the extra devices the user
	// had when it was priced — a second tap of the same button finds them changed.
	RequireExtra int
	// RequirePack likewise for the bought traffic (-1 = no check).
	RequirePack int64
}

// applyAddonOn writes an add-on, or ErrPlanStale when the user is no longer on the
// plan (or the term) it was bought for.
func applyAddonOn(ex execer, a AddonWrite) error {
	where, wargs := addonWhere(a)
	args := append([]any{a.AddDevices, a.AddDevices, a.AddData, a.AddData}, wargs...)
	res, err := ex.Exec(`UPDATE users SET device_limit = device_limit + ?, extra_devices = extra_devices + ?,
	          data_limit = data_limit + ?, pack_data = pack_data + ?
	      WHERE `+where, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPlanStale
	}
	return nil
}

// addonWhere is the condition an add-on is written under: the plan (and term) it was
// bought for, a cap and a quota to add to, room under the device cap, and the state it
// was priced on.
func addonWhere(a AddonWrite) (string, []any) {
	q := `id = ? AND plan_id = ?`
	args := []any{a.UserID, a.PlanID}
	if a.RequireExpire != 0 {
		q += ` AND expire_at = ?`
		args = append(args, a.RequireExpire)
	}
	// Devices onto a cap and traffic onto a quota: added to "unlimited" (0) they would
	// make one of just the add-on.
	if a.AddDevices > 0 {
		q += ` AND device_limit > 0 AND extra_devices + ? <= ?`
		args = append(args, a.AddDevices, a.MaxDevices)
	}
	if a.AddData > 0 {
		q += ` AND data_limit > 0`
	}
	if a.RequireExtra >= 0 {
		q += ` AND extra_devices = ?`
		args = append(args, a.RequireExtra)
	}
	if a.RequirePack >= 0 {
		q += ` AND pack_data = ?`
		args = append(args, a.RequirePack)
	}
	return q, args
}

// ApplyUserPlan writes a plan assignment atomically.
func (s *Store) ApplyUserPlan(p UserPlanWrite) error {
	return s.withTx(func(tx *sql.Tx) error { return applyUserPlanOn(tx, p) })
}

func applyUserPlanOn(ex *sql.Tx, p UserPlanWrite) error {
	if p.RequirePlan != 0 {
		var plan, exp int64
		if err := ex.QueryRow(`SELECT plan_id, expire_at FROM users WHERE id = ?`, p.UserID).Scan(&plan, &exp); err != nil {
			return err
		}
		if plan != p.RequirePlan || (p.RequireExpire != 0 && exp != p.RequireExpire) {
			return ErrPlanStale
		}
	}
	if err := setUserLimitsOn(ex, p.UserID, p.DataLimit, p.ExpireAt, p.DeviceLimit); err != nil {
		return err
	}
	if _, err := ex.Exec(`UPDATE users SET extra_devices = ?, pack_data = ? WHERE id = ?`,
		p.ExtraDevices, p.PackData, p.UserID); err != nil {
		return err
	}
	// A plan decides the term — a date, or none on a free plan — so a hold set by
	// hand goes with the rest of the manual limits. Left in place, the next
	// connection would start it and write its own date over the plan's.
	if _, err := ex.Exec(`UPDATE users SET hold_seconds = 0 WHERE id = ?`, p.UserID); err != nil {
		return err
	}
	// The speed cap is part of what the plan sells, so it is overwritten with the
	// rest of the limits — a user moved to a slower tariff must actually get slower.
	if _, err := ex.Exec(`UPDATE users SET `+planSpeedSet+` WHERE id = ?`, p.SpeedLimit, p.SpeedLimit, p.UserID); err != nil {
		return err
	}
	if err := setResetPeriodOn(ex, p.UserID, p.ResetPeriod, p.ResetAnchor); err != nil {
		return err
	}
	if err := setUserPlanOn(ex, p.UserID, p.PlanID, p.TrialUsed); err != nil {
		return err
	}
	if p.ResetUsage {
		if _, err := ex.Exec(
			`UPDATE users SET used_up = 0, used_down = 0, last_up = ?, last_down = ?
			 WHERE id = ?`, p.LastUp, p.LastDown, p.UserID,
		); err != nil {
			return err
		}
	}
	return setPlanGroupsOn(ex, p.UserID, p.GroupIDs)
}

// setPlanGroupsOn replaces the user's PLAN-granted group membership with groupIDs,
// leaving every hand-assigned membership alone. Called from inside the plan write, so
// "which plan" and "which groups" can never land apart.
func setPlanGroupsOn(ex execer, userID int64, groupIDs []int64) error {
	if _, err := ex.Exec(
		`DELETE FROM group_members WHERE user_id = ? AND via_plan = 1`, userID,
	); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, gid := range groupIDs {
		if gid == 0 || seen[gid] {
			continue
		}
		seen[gid] = true
		// DO NOTHING, not REPLACE: an existing row is a manual one, and re-stamping it
		// via_plan = 1 would hand the operator's own decision to the next plan switch
		// to undo.
		if _, err := ex.Exec(
			`INSERT INTO group_members (group_id, user_id, via_plan) VALUES (?, ?, 1)
			 ON CONFLICT (group_id, user_id) DO NOTHING`, gid, userID,
		); err != nil {
			return err
		}
	}
	return nil
}

// UserPlanGroups returns the groups a user is in BECAUSE of their plan, so a caller
// can tell whether a plan write actually changes access (and only then pay for the
// full reconcile that a membership change needs).
func (s *Store) UserPlanGroups(userID int64) ([]int64, error) {
	rows, err := s.rdb.Query(
		`SELECT group_id FROM group_members WHERE user_id = ? AND via_plan = 1 ORDER BY group_id`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ConfirmPaymentOrder claims the pending→paid transition AND applies the plan that
// payment bought, in a single transaction. It reports whether this caller won the
// claim — false means a concurrent webhook/poll already handled the order, and the
// plan was NOT applied again.
//
// The atomicity is the whole point, and it is specifically about crashes rather
// than concurrency (the CAS alone already handles concurrency). Done as separate
// statements, the terminal status commits FIRST and the plan LAST — so a process
// killed in between leaves an order marked paid whose plan was never granted. That
// state is unrecoverable by design: every retry path here keys off status =
// 'pending', which the claim has already overwritten, and a redelivered webhook
// no-ops on the non-pending status. Money captured, nothing delivered, nothing that
// can notice. In one transaction the pair is all-or-nothing: either the user has
// what they paid for, or the order is still pending and the 25s poll re-confirms it
// from the provider.
func (s *Store) ConfirmPaymentOrder(orderID, paidAt int64, p UserPlanWrite) (bool, error) {
	var claimed bool
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		if claimed, err = markPaidIfPendingOn(tx, orderID, paidAt); err != nil || !claimed {
			return err
		}
		return applyUserPlanOn(tx, p)
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
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

const orderCols = `o.id, o.user_id, u.name, o.plan_id, COALESCE(p.name, ''), o.amount_rub, o.status,
	o.provider, o.provider_id, o.pay_url, o.created_at, o.paid_at,
	o.kind, o.balance_kop, o.discount_rub, o.promo_id, COALESCE(pc.code, ''),
	o.periods, o.refunded_at, o.refund_source,
	o.devices, o.change_from, o.expect_expire, o.pack_bytes, o.devices_before`

// orderJoins resolves an order's user, plan and promo code. The plan is a LEFT join:
// a top-up has none.
const orderJoins = `
	JOIN users u ON u.id = o.user_id
	LEFT JOIN tariff_plans p ON p.id = o.plan_id
	LEFT JOIN promo_codes pc ON pc.id = o.promo_id`

func (s *Store) GetPaymentOrder(id int64) (*model.PaymentOrder, error) {
	orders, err := s.listPaymentOrders(
		`SELECT `+orderCols+`
		 FROM payment_orders o`+orderJoins+`
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
		 FROM payment_orders o`+orderJoins+`
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
		 FROM payment_orders o`+orderJoins+`
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

// LatestPaidOrder returns the user's newest order paid after since (or
// sql.ErrNoRows) — what a subscription page that watched a payment tells its user
// once it clears. Purchases from the balance are left out: nothing was waited on.
func (s *Store) LatestPaidOrder(userID, since int64) (*model.PaymentOrder, error) {
	orders, err := s.listPaymentOrders(
		`SELECT `+orderCols+`
		 FROM payment_orders o`+orderJoins+`
		 WHERE o.user_id = ? AND o.status = 'paid' AND o.paid_at > ? AND o.provider <> 'balance'
		 ORDER BY o.paid_at DESC, o.id DESC LIMIT 1`, userID, since)
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
		 FROM payment_orders o`+orderJoins+`
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
		 FROM payment_orders o`+orderJoins+`
		 WHERE o.provider = ? AND o.provider_id = ?`, provider, providerID)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, sql.ErrNoRows
	}
	return &orders[0], nil
}

func (s *Store) ListPaymentOrders(status string, userID int64, limit int) ([]model.PaymentOrder, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT ` + orderCols + `
	      FROM payment_orders o ` + orderJoins + ` WHERE 1 = 1`
	args := []any{}
	if status != "" {
		q += ` AND o.status = ?`
		args = append(args, status)
	}
	if userID > 0 {
		q += ` AND o.user_id = ?`
		args = append(args, userID)
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
// concurrent webhook just marked paid. It reports whether THIS call performed the
// cancellation — a caller that logs or notifies must not do so for an order someone
// else already resolved.
func (s *Store) CancelPaymentOrderIfPending(id int64) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE payment_orders SET status = 'cancelled', paid_at = 0 WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkPaymentOrderPaidIfPending atomically transitions an order pending→paid and
// reports whether THIS call performed the transition. Exactly one of several
// concurrent confirmers (provider webhook + the poll fallback + a re-delivered
// webhook) wins the CAS; a caller that gets false must not apply the plan, so a
// single payment can never extend the user twice.
func markPaidIfPendingOn(ex execer, id, paidAt int64) (bool, error) {
	res, err := ex.Exec(
		`UPDATE payment_orders SET status = 'paid', paid_at = ? WHERE id = ? AND status = 'pending'`,
		paidAt, id,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
			&o.Kind, &o.BalanceKop, &o.DiscountRub, &o.PromoID, &o.PromoCode,
			&o.Periods, &o.RefundedAt, &o.RefundSource,
			&o.Devices, &o.ChangeFrom, &o.ExpectExpire, &o.PackBytes, &o.DevicesBefore,
		); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) SetBillingSettings(st *model.Settings) error {
	_, err := s.db.Exec(
		`UPDATE settings SET billing_enabled = ?,
		 billing_free_plan_id = ?, billing_trial_plan_id = ?, billing_payment_note = ?,
		 billing_manual_enabled = ?, billing_manual_label = ?,
		 wallet_enabled = ?, wallet_topup_min = ?, billing_periods = ?,
		 ref_mode = ?, ref_percent = ?, ref_days = ?, ref_first_only = ?,
		 winback_enabled = ?, winback_after_days = ?, winback_percent = ?, winback_valid_days = ?,
		 traffic_packs = ?, plan_change = ?,
		 updated_at = unixepoch() WHERE id = 1`,
		boolToInt(st.BillingEnabled),
		st.BillingFreePlanID, st.BillingTrialPlanID, st.BillingPaymentNote,
		boolToInt(st.BillingManualEnabled), st.BillingManualLabel,
		boolToInt(st.WalletEnabled), st.WalletTopupMin, periodsJSON(st.BillingPeriods),
		st.RefMode, st.RefPercent, st.RefDays, boolToInt(st.RefFirstOnly),
		boolToInt(st.Winback.Enabled), st.Winback.AfterDays, st.Winback.Percent, st.Winback.ValidDays,
		packsJSON(st.TrafficPacks), boolToInt(st.PlanChange),
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
		 FROM payment_orders o`+orderJoins+`
		 WHERE o.status = 'pending' AND o.provider != '' AND o.provider_id != ''
		 ORDER BY o.created_at ASC LIMIT ?`, limit)
}

// periodsJSON stores the multi-period discounts; none is ”.
func periodsJSON(offers []model.PeriodOffer) string {
	if len(offers) == 0 {
		return ""
	}
	b, _ := json.Marshal(offers)
	return string(b)
}

// packsJSON is the traffic packs as stored ("" for none).
func packsJSON(p []model.TrafficPack) string {
	if len(p) == 0 {
		return ""
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// TakeBackAddon removes devices and traffic an order added, from a user still on the
// plan they were bought for — never below what the plan itself gives.
func (s *Store) TakeBackAddon(userID, planID int64, devices int, data int64) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE users SET
		     device_limit = device_limit - min(?, extra_devices),
		     extra_devices = extra_devices - min(?, extra_devices),
		     data_limit = data_limit - min(?, pack_data),
		     pack_data = pack_data - min(?, pack_data)
		 WHERE id = ? AND plan_id = ? AND ((? > 0 AND extra_devices > 0) OR (? > 0 AND pack_data > 0))`,
		devices, devices, data, data, userID, planID, devices, data)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// PlanPaidPerPeriodKop is what the user last paid for one period of plan, with its
// extra devices, in kopecks — money and balance together; found false when they
// never bought it (it was assigned, given by a code, or reached by a change).
func (s *Store) PlanPaidPerPeriodKop(userID, planID int64) (int64, bool) {
	var kop, periods int64
	err := s.db.QueryRow(
		`SELECT amount_rub * 100 + balance_kop, max(periods, 1) FROM payment_orders
		 WHERE user_id = ? AND plan_id = ? AND status = 'paid' AND kind = 'plan' AND refunded_at = 0
		 ORDER BY paid_at DESC, id DESC LIMIT 1`, userID, planID).Scan(&kop, &periods)
	if err != nil || periods <= 0 {
		return 0, false
	}
	return kop / periods, true
}

// ReachedByChange reports whether the user's move onto plan was a paid-for change —
// the difference was paid at the plan's list price.
func (s *Store) ReachedByChange(userID, planID int64) bool {
	var n int
	_ = s.db.QueryRow(
		`SELECT count(*) FROM payment_orders
		 WHERE user_id = ? AND plan_id = ? AND status = 'paid' AND kind = 'change' AND refunded_at = 0`,
		userID, planID).Scan(&n)
	return n > 0
}
