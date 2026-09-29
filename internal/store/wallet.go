package store

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The wallet: a balance per user, the ledger behind it, promo codes and referrals.
// See migrations/0086_wallet_promo_referral.sql for the columns.
//
// Every balance change goes through credit/debit below, inside the transaction of
// whatever caused it, so the ledger and the balance cannot disagree.

var (
	// ErrInsufficientBalance is a debit the balance does not cover.
	ErrInsufficientBalance = errors.New("insufficient balance")
	// ErrPromoUnavailable is a code that is off, expired or used up.
	ErrPromoUnavailable = errors.New("promo code unavailable")
	// ErrPromoUsed is a code this user has already used.
	ErrPromoUsed = errors.New("promo code already used")
	// ErrTooManyPending is an order refused because the user already has as many of
	// its kind waiting as the caller allows.
	ErrTooManyPending = errors.New("too many pending orders")
)

// txEntry is what a ledger line records besides the amount.
type txEntry struct {
	Kind      string
	OrderID   int64
	RefUserID int64
	PromoID   int64
	Note      string
	Now       int64
}

// credit adds kop to the user's balance and writes the ledger line. It reports false
// when the user does not exist (a referrer deleted since), writing nothing.
func credit(tx *sql.Tx, userID, kop int64, e txEntry) (bool, error) {
	var bal int64
	err := tx.QueryRow(
		`UPDATE users SET balance_kop = balance_kop + ? WHERE id = ? RETURNING balance_kop`,
		kop, userID).Scan(&bal)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, ledger(tx, userID, kop, bal, e)
}

// debit takes kop off the balance when it covers it, and reports whether it did. The
// check and the write are one statement, so two debits racing cannot both pass it.
func debit(tx *sql.Tx, userID, kop int64, e txEntry) (bool, error) {
	var bal int64
	err := tx.QueryRow(
		`UPDATE users SET balance_kop = balance_kop - ?
		 WHERE id = ? AND balance_kop >= ? RETURNING balance_kop`,
		kop, userID, kop).Scan(&bal)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, ledger(tx, userID, -kop, bal, e)
}

func ledger(tx *sql.Tx, userID, amount, balance int64, e txEntry) error {
	if amount == 0 {
		return nil
	}
	_, err := tx.Exec(
		`INSERT INTO balance_tx (user_id, amount_kop, balance_kop, kind, order_id,
		     ref_user_id, promo_id, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, amount, balance, e.Kind, e.OrderID, e.RefUserID, e.PromoID, e.Note, e.Now)
	return err
}

// OrderDraft is a new payment order.
type OrderDraft struct {
	UserID      int64
	PlanID      int64 // 0 for a top-up
	Kind        string
	AmountRub   int   // money to collect
	BalanceKop  int64 // the part of the price the balance covers
	DiscountRub int
	PromoID     int64
	// PendingCap, when above 0, refuses the order (ErrTooManyPending) while the user
	// already has that many pending orders of the same kind created after
	// PendingSince. Checked in the insert itself, so parallel requests cannot all pass.
	PendingCap   int
	PendingSince int64
	Periods      int // of the plan's periods bought; 0 means 1
	// What the order buys beyond a plan and its periods (see PaymentOrder).
	Devices      int
	ChangeFrom   int64
	ExpectExpire int64
	PackBytes    int64
}

// CreateOrder inserts a pending order.
func (s *Store) CreateOrder(d OrderDraft, now int64) (*model.PaymentOrder, error) {
	if d.Kind == "" {
		d.Kind = model.OrderPlan
	}
	if d.Periods < 1 {
		d.Periods = 1
	}
	limit := d.PendingCap
	if limit <= 0 {
		limit = 1 << 30
	}
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO payment_orders (user_id, plan_id, amount_rub, status, created_at,
		     kind, balance_kop, discount_rub, promo_id, periods,
		     devices, change_from, expect_expire, pack_bytes)
		 SELECT ?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE (SELECT count(*) FROM payment_orders
		        WHERE user_id = ? AND kind = ? AND status = 'pending' AND created_at > ?) < ?
		 RETURNING id`,
		d.UserID, d.PlanID, d.AmountRub, now, d.Kind, d.BalanceKop, d.DiscountRub, d.PromoID, d.Periods,
		d.Devices, d.ChangeFrom, d.ExpectExpire, d.PackBytes,
		d.UserID, d.Kind, d.PendingSince, limit,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTooManyPending
	}
	if err != nil {
		return nil, err
	}
	return s.GetPaymentOrder(id)
}

// RefReward is the referral programme as it stands when an order is confirmed.
type RefReward struct {
	Mode      string
	Percent   int
	Days      int
	FirstOnly bool
}

// ConfirmSpec is everything a paid order delivers, committed with the claim.
type ConfirmSpec struct {
	// Plan is the plan the order buys; nil for a top-up. Addon, instead of Plan, is
	// devices or traffic added to the plan the user holds.
	Plan  *UserPlanWrite
	Addon *AddonWrite
	// CreditKop is money that lands on the balance: a top-up, or the paid part of an
	// order the balance helps pay for. DebitKop is then the full price taken back off
	// the balance before the plan is granted.
	CreditKop int64
	DebitKop  int64
	// BonusDays are the banked referral days Plan already includes.
	BonusDays int
	PromoID   int64
	Ref       RefReward
	Now       int64
	// AsTopup settles a plan order as a top-up (Plan nil): its plan is no longer one
	// to grant, so the money only lands on the balance.
	AsTopup bool
}

// ConfirmResult says what a confirmation did.
type ConfirmResult struct {
	Claimed bool
	// PlanApplied is false when the balance no longer covered its part of the price by
	// the time the money arrived: the money is then on the balance, and no plan.
	PlanApplied bool
	RefUserID   int64
	RefKop      int64
	RefDays     int
	RefBanked   bool // RefDays went to the referrer's bank, not onto a plan
}

// ConfirmOrder claims the pending→paid transition and delivers everything the payment
// bought in one transaction — the reason ConfirmPaymentOrder exists, extended to the
// balance, the promo code and the referral reward.
func (s *Store) ConfirmOrder(orderID, paidAt int64, spec ConfirmSpec) (ConfirmResult, error) {
	var res ConfirmResult
	err := s.withTx(func(tx *sql.Tx) error {
		claimed, err := markPaidIfOpenOn(tx, orderID, paidAt)
		if err != nil || !claimed {
			return err
		}
		res.Claimed = true
		var userID int64
		var amountRub int
		var kind string
		if err := tx.QueryRow(`SELECT user_id, amount_rub, kind FROM payment_orders WHERE id = ?`, orderID).
			Scan(&userID, &amountRub, &kind); err != nil {
			return err
		}
		if spec.CreditKop > 0 {
			if _, err := credit(tx, userID, spec.CreditKop,
				txEntry{Kind: model.TxTopup, OrderID: orderID, Now: spec.Now}); err != nil {
				return err
			}
		}
		if spec.AsTopup {
			if _, err := tx.Exec(`UPDATE payment_orders SET kind = 'topup' WHERE id = ?`, orderID); err != nil {
				return err
			}
		}
		if spec.Plan != nil || spec.Addon != nil {
			// A change or an add-on priced for a term the user no longer has delivers
			// nothing — the price was for that term — and nothing is debited for it.
			ok, err := deliverableOn(tx, spec.Plan, spec.Addon)
			if err != nil {
				return err
			}
			if ok && spec.DebitKop > 0 {
				if ok, err = debit(tx, userID, spec.DebitKop,
					txEntry{Kind: model.TxPurchase, OrderID: orderID, PromoID: spec.PromoID, Now: spec.Now}); err != nil {
					return err
				}
			}
			if ok {
				// The money is in: a code used meanwhile still delivers what was paid for.
				if spec.Addon != nil {
					err = applyAddonOn(tx, *spec.Addon)
				} else {
					_, err = deliverPlan(tx, userID, *spec.Plan, spec.BonusDays, spec.PromoID, orderID, spec.Now, false)
				}
				if err != nil {
					return err
				}
				res.PlanApplied = true
			} else {
				// No plan came of it: the money is on the balance, so the order is a
				// top-up — and must not count as a plan bought (first-purchase codes,
				// the first-only referral reward).
				if _, err := tx.Exec(`UPDATE payment_orders SET kind = 'topup' WHERE id = ?`, orderID); err != nil {
					return err
				}
			}
		}
		price := spec.DebitKop
		if price == 0 {
			price = int64(amountRub) * 100
		}
		return rewardReferrer(tx, userID, orderID, paidFor{
			money: amountRub, plan: kind == model.OrderPlan && res.PlanApplied, priceKop: price,
		}, spec.Ref, spec.Now, &res)
	})
	if err != nil {
		return ConfirmResult{}, err
	}
	return res, nil
}

// markPaidIfOpenOn claims an order paid while it is still pending OR cancelled: the
// money is in, and a cancelled order (superseded by a newer checkout, swept as
// abandoned, cancelled by hand) is still what that money bought — cancelling first
// and losing the payment later is exactly the trap this closes. A paid order stays
// paid, so a re-delivered webhook claims nothing.
func markPaidIfOpenOn(ex execer, id, paidAt int64) (bool, error) {
	res, err := ex.Exec(
		`UPDATE payment_orders SET status = 'paid', paid_at = ?
		 WHERE id = ? AND status IN ('pending', 'cancelled')`, paidAt, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// deliverableOn reports whether a plan write or an add-on still fits the user it was
// priced for (always, when it names no term to hold to).
func deliverableOn(tx *sql.Tx, w *UserPlanWrite, a *AddonWrite) (bool, error) {
	var userID, plan, exp int64
	switch {
	case a != nil:
		userID, plan, exp = a.UserID, a.PlanID, a.RequireExpire
	case w != nil && w.RequirePlan != 0:
		userID, plan, exp = w.UserID, w.RequirePlan, w.RequireExpire
	default:
		return true, nil
	}
	var curPlan, curExp int64
	err := tx.QueryRow(`SELECT plan_id, expire_at FROM users WHERE id = ?`, userID).Scan(&curPlan, &curExp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return curPlan == plan && (exp == 0 || curExp == exp), nil
}

// deliverPlan writes a bought plan, spends the banked referral days it includes and
// uses up the promo code that discounted it. It reports whether that use was this
// order's own (false: the user had already used the code). strict refuses a code that
// is used up (ErrPromoUnavailable) — for a purchase no money has been paid for yet.
func deliverPlan(tx *sql.Tx, userID int64, w UserPlanWrite, bonusDays int, promoID, orderID, now int64, strict bool) (bool, error) {
	if err := applyUserPlanOn(tx, w); err != nil {
		return false, err
	}
	if bonusDays > 0 {
		if _, err := tx.Exec(
			`UPDATE users SET ref_bonus_days = max(ref_bonus_days - ?, 0) WHERE id = ?`,
			bonusDays, userID); err != nil {
			return false, err
		}
	}
	if promoID == 0 {
		return true, nil
	}
	// No limit check here: the price was quoted with the code and the money is in.
	// A code that ran out meanwhile goes one use over rather than taking the discount
	// back from someone who already paid.
	r, err := tx.Exec(
		`INSERT INTO promo_uses (promo_id, user_id, order_id, used_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT DO NOTHING`, promoID, userID, orderID, now)
	if err != nil {
		return false, err
	}
	fresh := false
	if n, _ := r.RowsAffected(); n > 0 {
		fresh = true
		q := `UPDATE promo_codes SET uses = uses + 1 WHERE id = ?`
		if strict {
			q += ` AND (max_uses = 0 OR uses < max_uses)`
		}
		u, err := tx.Exec(q, promoID)
		if err != nil {
			return false, err
		}
		if n, _ := u.RowsAffected(); n == 0 && strict {
			return false, ErrPromoUnavailable
		}
	}
	_, err = tx.Exec(`UPDATE users SET promo_id = 0 WHERE id = ? AND promo_id = ?`, userID, promoID)
	return fresh, err
}

// paidFor is what an order paid for, as the referral reward sees it: money brought
// in, and whether it bought a plan.
type paidFor struct {
	money    int   // roubles from outside
	plan     bool  // a plan purchase that was delivered, with money or from the balance
	priceKop int64 // what the plan cost in all, money and balance together
}

// rewardReferrer pays the referrer of userID for an order, and only ever for money
// that came in. A percentage is of that money (a top-up included — the money is
// real). Days are for a plan bought with money: days per top-up would pay for money
// the invitee keeps, and days for a plan bought from the balance would pay for
// balance a promo code gave away.
func rewardReferrer(tx *sql.Tx, userID, orderID int64, paid paidFor, ref RefReward, now int64, res *ConfirmResult) error {
	switch {
	case paid.money <= 0:
		return nil
	case ref.Mode == model.RefPercent:
	// Days for a plan paid mostly with money: a rouble on top of a gifted balance
	// must not earn a whole bonus period.
	case ref.Mode == model.RefDays && paid.plan && int64(paid.money)*200 >= paid.priceKop:
	default:
		return nil
	}
	var referrer int64
	if err := tx.QueryRow(`SELECT referrer_id FROM users WHERE id = ?`, userID).Scan(&referrer); err != nil {
		return err
	}
	if referrer == 0 || referrer == userID {
		return nil
	}
	if ref.FirstOnly {
		// The first of what the mode pays for: money in, or a plan bought.
		earlierQ := `SELECT count(*) FROM payment_orders
			 WHERE user_id = ? AND status = 'paid' AND amount_rub > 0 AND id <> ?`
		if ref.Mode == model.RefDays {
			earlierQ = `SELECT count(*) FROM payment_orders
			 WHERE user_id = ? AND status = 'paid' AND kind = 'plan' AND amount_rub > 0 AND id <> ?`
		}
		var earlier int
		if err := tx.QueryRow(earlierQ, userID, orderID).Scan(&earlier); err != nil {
			return err
		}
		if earlier > 0 {
			return nil
		}
	}
	switch ref.Mode {
	case model.RefPercent:
		kop := int64(paid.money) * int64(ref.Percent) // amount × 100 kop × percent / 100
		if kop <= 0 {
			return nil
		}
		ok, err := credit(tx, referrer, kop,
			txEntry{Kind: model.TxReferral, OrderID: orderID, RefUserID: userID, Now: now})
		if err != nil || !ok {
			return err
		}
		res.RefUserID, res.RefKop = referrer, kop
	case model.RefDays:
		if ref.Days <= 0 {
			return nil
		}
		// Onto a running paid plan with a term; otherwise banked for the next one.
		r, err := tx.Exec(
			`UPDATE users SET expire_at = expire_at + ?
			 WHERE id = ? AND expire_at > ? AND plan_id IN (
			     SELECT id FROM tariff_plans WHERE price_rub > 0 AND period_days > 0)`,
			int64(ref.Days)*86400, referrer, now)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			// A referrer on a lifetime plan has no term for days to extend, now or
			// later (they cannot switch plans while it lasts): banking would promise
			// days they can never use.
			var lifetime int
			if err := tx.QueryRow(
				`SELECT count(*) FROM users u JOIN tariff_plans p ON p.id = u.plan_id
				 WHERE u.id = ? AND p.price_rub > 0 AND p.period_days = 0`, referrer).Scan(&lifetime); err != nil {
				return err
			}
			if lifetime > 0 {
				return nil
			}
			r, err = tx.Exec(`UPDATE users SET ref_bonus_days = ref_bonus_days + ? WHERE id = ?`, ref.Days, referrer)
			if err != nil {
				return err
			}
			if n, _ := r.RowsAffected(); n == 0 {
				return nil // referrer deleted
			}
			res.RefBanked = true
		}
		if _, err := tx.Exec(`UPDATE payment_orders SET ref_days = ? WHERE id = ?`, ref.Days, orderID); err != nil {
			return err
		}
		res.RefUserID, res.RefDays = referrer, ref.Days
	}
	return nil
}

// BalancePurchase is a plan bought from the balance alone.
type BalancePurchase struct {
	UserID      int64
	PlanID      int64
	PriceKop    int64 // what the balance pays (after the discount)
	DiscountRub int
	PromoID     int64
	Plan        UserPlanWrite
	BonusDays   int
	Kind        string // TxPurchase or TxRenew
	Periods     int    // of the plan's periods bought; 0 means 1
	Now         int64
	// Addon, instead of Plan, adds to the plan the user holds. Order carries what the
	// order row records beyond a plan purchase: its kind and the add-on's details.
	Addon *AddonWrite
	Order OrderDraft
}

// BuyFromBalance debits the balance, records the purchase as a paid order and grants
// the plan, all or nothing. ErrInsufficientBalance when the balance falls short,
// ErrPromoUsed / ErrPromoUnavailable when the discount code was used meanwhile —
// nothing is taken then, and the price without it can be asked for again. No referral
// reward: no money came in.
func (s *Store) BuyFromBalance(p BalancePurchase) (int64, error) {
	var orderID int64
	err := s.withTx(func(tx *sql.Tx) error {
		kind := p.Order.Kind
		if kind == "" {
			kind = model.OrderPlan
		}
		if err := tx.QueryRow(
			`INSERT INTO payment_orders (user_id, plan_id, amount_rub, status, created_at, paid_at,
			     provider, kind, balance_kop, discount_rub, promo_id, periods,
			     devices, change_from, expect_expire, pack_bytes)
			 VALUES (?, ?, 0, 'paid', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			p.UserID, p.PlanID, p.Now, p.Now, model.BalanceProvider, kind,
			p.PriceKop, p.DiscountRub, p.PromoID, max(p.Periods, 1),
			p.Order.Devices, p.Order.ChangeFrom, p.Order.ExpectExpire, p.Order.PackBytes,
		).Scan(&orderID); err != nil {
			return err
		}
		plan := &p.Plan
		if p.Addon != nil {
			plan = nil
		}
		if ok, err := deliverableOn(tx, plan, p.Addon); err != nil {
			return err
		} else if !ok {
			return ErrPlanStale
		}
		ok, err := debit(tx, p.UserID, p.PriceKop,
			txEntry{Kind: p.Kind, OrderID: orderID, PromoID: p.PromoID, Now: p.Now})
		if err != nil {
			return err
		}
		if !ok {
			return ErrInsufficientBalance
		}
		if p.Addon != nil {
			return applyAddonOn(tx, *p.Addon)
		}
		fresh, err := deliverPlan(tx, p.UserID, p.Plan, p.BonusDays, p.PromoID, orderID, p.Now, true)
		if err != nil {
			return err
		}
		if !fresh {
			return ErrPromoUsed
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return orderID, nil
}

// PromoPendingHolders is how many OTHER users hold a pending order discounted by the
// code, created after since — uses already spoken for, so a limited code is not
// promised to more checkouts than it has uses left.
func (s *Store) PromoPendingHolders(promoID, exceptUser, since int64) int {
	var n int
	_ = s.rdb.QueryRow(
		`SELECT count(DISTINCT user_id) FROM payment_orders
		 WHERE promo_id = ? AND status = 'pending' AND user_id <> ? AND created_at > ?`,
		promoID, exceptUser, since).Scan(&n)
	return n
}

// CancelPendingOrdersWithPromo cancels the user's still-pending orders that carry a
// discount code, except keep, and returns them. A code serves one checkout at a time:
// a new one supersedes the old, or two open invoices could each be paid at the
// discount it gives once.
func (s *Store) CancelPendingOrdersWithPromo(userID, promoID, keep int64) ([]model.PaymentOrder, error) {
	list, err := s.listPaymentOrders(
		`SELECT `+orderCols+` FROM payment_orders o`+orderJoins+`
		 WHERE o.user_id = ? AND o.promo_id = ? AND o.status = 'pending' AND o.id <> ?`,
		userID, promoID, keep)
	if err != nil {
		return nil, err
	}
	var out []model.PaymentOrder
	for _, o := range list {
		if ok, err := s.CancelPaymentOrderIfPending(o.ID); err == nil && ok {
			out = append(out, o)
		}
	}
	return out, nil
}

// AdjustBalance is an operator's correction: delta kopecks (either sign) with a note.
// A debit may not take the balance below zero. Returns the new balance.
func (s *Store) AdjustBalance(userID, delta int64, note string, now int64) (int64, error) {
	var bal int64
	err := s.withTx(func(tx *sql.Tx) error {
		e := txEntry{Kind: model.TxAdmin, Note: note, Now: now}
		var ok bool
		var err error
		if delta >= 0 {
			ok, err = credit(tx, userID, delta, e)
			if err == nil && !ok {
				err = sql.ErrNoRows
			}
		} else {
			ok, err = debit(tx, userID, -delta, e)
			if err == nil && !ok {
				err = ErrInsufficientBalance
			}
		}
		if err != nil {
			return err
		}
		return tx.QueryRow(`SELECT balance_kop FROM users WHERE id = ?`, userID).Scan(&bal)
	})
	return bal, err
}

// GetWallet reads a user's balance and referral standing.
func (s *Store) GetWallet(userID int64) (model.Wallet, error) {
	var w model.Wallet
	var autoRenew int
	err := s.rdb.QueryRow(
		`SELECT u.balance_kop, u.auto_renew, u.referrer_id, COALESCE(r.name, ''), u.ref_code,
		        u.ref_bonus_days, u.promo_id, COALESCE(pc.code, ''),
		        (SELECT count(*) FROM users WHERE referrer_id = u.id AND referrer_id <> 0),
		        (SELECT count(DISTINCT o.user_id) FROM users x CROSS JOIN payment_orders o ON o.user_id = x.id
		         WHERE x.referrer_id = u.id AND x.referrer_id <> 0 AND o.status = 'paid' AND o.amount_rub > 0),
		        (SELECT COALESCE(sum(amount_kop), 0) FROM balance_tx
		         WHERE user_id = u.id AND kind = 'referral')
		 FROM users u
		 LEFT JOIN users r ON r.id = u.referrer_id
		 LEFT JOIN promo_codes pc ON pc.id = u.promo_id
		 WHERE u.id = ?`, userID,
	).Scan(&w.BalanceKop, &autoRenew, &w.ReferrerID, &w.ReferrerName, &w.RefCode,
		&w.RefBonusDays, &w.PromoID, &w.PromoCode, &w.Invited, &w.Paying, &w.EarnedKop)
	w.AutoRenew = autoRenew != 0
	return w, err
}

// GetWalletLite reads only the user's own wallet columns — balance, auto-renew,
// banked days, the attached code — without GetWallet's referral counts: what pricing
// and renewal need, on paths that run for every plan shown.
func (s *Store) GetWalletLite(userID int64) (model.Wallet, error) {
	var w model.Wallet
	var autoRenew int
	err := s.rdb.QueryRow(
		`SELECT balance_kop, auto_renew, referrer_id, ref_bonus_days, promo_id FROM users WHERE id = ?`, userID,
	).Scan(&w.BalanceKop, &autoRenew, &w.ReferrerID, &w.RefBonusDays, &w.PromoID)
	w.AutoRenew = autoRenew != 0
	return w, err
}

// SetAutoRenew turns renewal from the balance on or off for a user.
func (s *Store) SetAutoRenew(userID int64, on bool) error {
	_, err := s.db.Exec(`UPDATE users SET auto_renew = ? WHERE id = ?`, boolToInt(on), userID)
	return err
}

// EnsureRefCode returns the user's invite code, minting one with gen the first time.
// A generated code that collides with another user's is drawn again.
func (s *Store) EnsureRefCode(userID int64, gen func() string) (string, error) {
	for range 5 {
		var code string
		if err := s.db.QueryRow(`SELECT ref_code FROM users WHERE id = ?`, userID).Scan(&code); err != nil {
			return "", err
		}
		if code != "" {
			return code, nil
		}
		_, err := s.db.Exec(`UPDATE users SET ref_code = ? WHERE id = ? AND ref_code = ''`, gen(), userID)
		if err != nil && !strings.Contains(err.Error(), "UNIQUE") {
			return "", err
		}
	}
	return "", errors.New("could not mint a unique invite code")
}

// UserIDByRefCode resolves an invite code, or 0.
func (s *Store) UserIDByRefCode(code string) int64 {
	if code == "" {
		return 0
	}
	var id int64
	_ = s.rdb.QueryRow(`SELECT id FROM users WHERE ref_code = ?`, code).Scan(&id)
	return id
}

// SetReferrer records refID as the user's referrer — only while they have none, and
// not the user themselves or someone the user invited. Reports whether it was set.
func (s *Store) SetReferrer(userID, refID int64) (bool, error) {
	r, err := s.db.Exec(
		`UPDATE users SET referrer_id = ?
		 WHERE id = ? AND referrer_id = 0 AND ? <> id
		   AND EXISTS (SELECT 1 FROM users r WHERE r.id = ? AND r.referrer_id <> ?)`,
		refID, userID, refID, refID, userID)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// SetSubscriberRef remembers who invited a chat that has not registered yet. The first
// invite a chat followed is the one that counts.
func (s *Store) SetSubscriberRef(chatID, refUserID, now int64) error {
	_, err := s.db.Exec(
		`INSERT INTO tg_subscribers (chat_id, ref_user_id, started_at) VALUES (?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET ref_user_id = excluded.ref_user_id
		 WHERE tg_subscribers.ref_user_id = 0`,
		chatID, refUserID, now)
	return err
}

// AttachReferrerFromChat makes the referrer the chat arrived with the new user's
// referrer. Only once, never the user themselves, and only while that referrer exists.
func (s *Store) AttachReferrerFromChat(userID, chatID int64) (int64, error) {
	var ref int64
	err := s.db.QueryRow(
		`UPDATE users SET referrer_id = (SELECT ref_user_id FROM tg_subscribers WHERE chat_id = ?)
		 WHERE id = ? AND referrer_id = 0
		   AND EXISTS (SELECT 1 FROM tg_subscribers t JOIN users r ON r.id = t.ref_user_id
		               WHERE t.chat_id = ? AND t.ref_user_id <> ?)
		 RETURNING referrer_id`,
		chatID, userID, chatID, userID).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return ref, err
}

// ListBalanceTx returns a user's newest ledger lines.
func (s *Store) ListBalanceTx(userID int64, limit int) ([]model.BalanceTx, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.rdb.Query(
		`SELECT t.id, t.user_id, t.amount_kop, t.balance_kop, t.kind, t.order_id,
		        t.ref_user_id, COALESCE(r.name, ''), t.promo_id, COALESCE(pc.code, ''),
		        COALESCE(p.name, ''), t.note, t.created_at
		 FROM balance_tx t
		 LEFT JOIN users r ON r.id = t.ref_user_id
		 LEFT JOIN promo_codes pc ON pc.id = t.promo_id
		 LEFT JOIN payment_orders o ON o.id = t.order_id AND t.kind IN ('purchase', 'renew')
		 LEFT JOIN tariff_plans p ON p.id = o.plan_id
		 WHERE t.user_id = ? ORDER BY t.id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.BalanceTx{}
	for rows.Next() {
		var t model.BalanceTx
		if err := rows.Scan(&t.ID, &t.UserID, &t.AmountKop, &t.BalanceKop, &t.Kind, &t.OrderID,
			&t.RefUserID, &t.RefName, &t.PromoID, &t.PromoCode, &t.PlanName, &t.Note, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PaidPlanOrdersOffBalance returns the user's newest plan purchases paid wholly with
// money — they never touch the balance ledger, but belong in a history of what the
// user paid.
func (s *Store) PaidPlanOrdersOffBalance(userID int64, limit int) ([]model.PaymentOrder, error) {
	return s.listPaymentOrders(
		`SELECT `+orderCols+`
		 FROM payment_orders o`+orderJoins+`
		 WHERE o.user_id = ? AND o.status = 'paid' AND o.kind = 'plan'
		   AND o.balance_kop = 0 AND o.amount_rub > 0
		 ORDER BY o.paid_at DESC LIMIT ?`, userID, limit)
}

// UsersDueRenewal returns users whose paid plan with a term ends between since and
// horizon, who are switched on, left renewal on and whose balance covers the plan's
// price. since keeps a plan that ran out long ago from being bought again by money
// meant for something else.
func (s *Store) UsersDueRenewal(since, horizon int64) ([]model.User, error) {
	return s.queryUsers(
		`SELECT `+userCols+` FROM users WHERE id IN (
		     SELECT u.id FROM users u JOIN tariff_plans p ON p.id = u.plan_id
		     WHERE p.price_rub > 0 AND p.period_days > 0 AND u.auto_renew = 1 AND u.enabled = 1
		       AND u.expire_at > ? AND u.expire_at <= ?
		       AND u.balance_kop >= p.price_rub * 100)`, since, horizon)
}

// HasBoughtPlan reports whether the user ever bought a plan, with money or from the
// balance — what a first-purchase discount asks.
func (s *Store) HasBoughtPlan(userID int64) (bool, error) {
	var n int
	err := s.rdb.QueryRow(
		`SELECT count(*) FROM payment_orders WHERE user_id = ? AND status = 'paid' AND kind = 'plan'`,
		userID).Scan(&n)
	return n > 0, err
}

// HasPaid reports whether the user ever paid money (a balance purchase is not money).
func (s *Store) HasPaid(userID int64) (bool, error) {
	var n int
	err := s.rdb.QueryRow(
		`SELECT count(*) FROM payment_orders WHERE user_id = ? AND status = 'paid' AND amount_rub > 0`,
		userID).Scan(&n)
	return n > 0, err
}

// --- promo codes ---

const promoCols = `id, code, kind, value, plan_ids, plan_id, first_only, max_uses, uses,
	expires_at, enabled, note, created_at`

func scanPromo(sc interface{ Scan(...any) error }) (model.PromoCode, error) {
	var p model.PromoCode
	var planIDs string
	var firstOnly, enabled int
	err := sc.Scan(&p.ID, &p.Code, &p.Kind, &p.Value, &planIDs, &p.PlanID, &firstOnly,
		&p.MaxUses, &p.Uses, &p.ExpiresAt, &enabled, &p.Note, &p.CreatedAt)
	p.PlanIDs = parseIDList(planIDs)
	p.FirstOnly, p.Enabled = firstOnly != 0, enabled != 0
	return p, err
}

func parseIDList(s string) []int64 {
	out := []int64{}
	for _, f := range strings.Split(s, ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func joinIDList(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

// ListPromos returns every promo code, newest first.
func (s *Store) ListPromos() ([]model.PromoCode, error) {
	rows, err := s.rdb.Query(`SELECT ` + promoCols + ` FROM promo_codes WHERE winback_user = 0 ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.PromoCode{}
	for rows.Next() {
		p, err := scanPromo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPromo reads one code by id.
func (s *Store) GetPromo(id int64) (model.PromoCode, error) {
	return scanPromo(s.rdb.QueryRow(`SELECT `+promoCols+` FROM promo_codes WHERE id = ?`, id))
}

// GetPromoByCode reads one code by its text, ignoring case.
func (s *Store) GetPromoByCode(code string) (model.PromoCode, error) {
	return scanPromo(s.rdb.QueryRow(`SELECT `+promoCols+` FROM promo_codes WHERE code = ?`, code))
}

// CountEnabledPromos is how many codes are on — the bot offers the promo button only
// when there is something to enter.
func (s *Store) CountEnabledPromos() int {
	var n int
	// Live codes only: a spent or expired one — every win-back code in time — must not
	// keep the promo field up for everyone.
	_ = s.rdb.QueryRow(
		`SELECT count(*) FROM promo_codes WHERE enabled = 1
		   AND (expires_at = 0 OR expires_at > unixepoch()) AND (max_uses = 0 OR uses < max_uses)`).Scan(&n)
	return n
}

// SavePromo inserts (ID 0) or updates a code. Uses and the creation time are the
// store's own and are not taken from p.
func (s *Store) SavePromo(p *model.PromoCode, now int64) error {
	if p.ID == 0 {
		return s.db.QueryRow(
			`INSERT INTO promo_codes (code, kind, value, plan_ids, plan_id, first_only,
			     max_uses, expires_at, enabled, note, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id, created_at`,
			p.Code, p.Kind, p.Value, joinIDList(p.PlanIDs), p.PlanID, boolToInt(p.FirstOnly),
			p.MaxUses, p.ExpiresAt, boolToInt(p.Enabled), p.Note, now,
		).Scan(&p.ID, &p.CreatedAt)
	}
	r, err := s.db.Exec(
		`UPDATE promo_codes SET code = ?, kind = ?, value = ?, plan_ids = ?, plan_id = ?,
		     first_only = ?, max_uses = ?, expires_at = ?, enabled = ?, note = ?
		 WHERE id = ?`,
		p.Code, p.Kind, p.Value, joinIDList(p.PlanIDs), p.PlanID, boolToInt(p.FirstOnly),
		p.MaxUses, p.ExpiresAt, boolToInt(p.Enabled), p.Note, p.ID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeletePromo removes a code. Orders and ledger lines keep its id and lose its name;
// a discount waiting on a user goes with it.
func (s *Store) DeletePromo(id int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE users SET promo_id = 0 WHERE promo_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM promo_uses WHERE promo_id = ?`, id); err != nil {
			return err
		}
		r, err := tx.Exec(`DELETE FROM promo_codes WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// PromoUsedBy reports whether the user already used the code.
func (s *Store) PromoUsedBy(promoID, userID int64) bool {
	var n int
	_ = s.rdb.QueryRow(`SELECT count(*) FROM promo_uses WHERE promo_id = ? AND user_id = ?`,
		promoID, userID).Scan(&n)
	return n > 0
}

// SetUserPromo attaches a discount code to the user's next payment (0 detaches it).
func (s *Store) SetUserPromo(userID, promoID int64) error {
	_, err := s.db.Exec(`UPDATE users SET promo_id = ? WHERE id = ?`, promoID, userID)
	return err
}

// redeemPromo takes one use of a code for a user — refusing a code that is off,
// expired, used up or already used by them — and runs apply in the same transaction.
func (s *Store) redeemPromo(promoID, userID, now int64, apply func(tx *sql.Tx) error) error {
	return s.withTx(func(tx *sql.Tx) error {
		r, err := tx.Exec(
			`UPDATE promo_codes SET uses = uses + 1
			 WHERE id = ? AND enabled = 1 AND (expires_at = 0 OR expires_at > ?)
			   AND (max_uses = 0 OR uses < max_uses)`, promoID, now)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrPromoUnavailable
		}
		r, err = tx.Exec(
			`INSERT INTO promo_uses (promo_id, user_id, used_at) VALUES (?, ?, ?)
			 ON CONFLICT DO NOTHING`, promoID, userID, now)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrPromoUsed
		}
		return apply(tx)
	})
}

// RedeemPromoBalance puts a balance code's roubles on the user's balance.
func (s *Store) RedeemPromoBalance(promoID, userID, kop, now int64) error {
	return s.redeemPromo(promoID, userID, now, func(tx *sql.Tx) error {
		ok, err := credit(tx, userID, kop, txEntry{Kind: model.TxPromo, PromoID: promoID, Now: now})
		if err == nil && !ok {
			err = sql.ErrNoRows
		}
		return err
	})
}

// RedeemPromoPlan grants a days code's plan write.
func (s *Store) RedeemPromoPlan(promoID, userID int64, w UserPlanWrite, now int64) error {
	return s.redeemPromo(promoID, userID, now, func(tx *sql.Tx) error {
		return applyUserPlanOn(tx, w)
	})
}

// forgetWalletOn clears what the wallet keeps about users being deleted: their ledger
// and promo uses go, and anyone they invited stops pointing at an id nothing reuses.
func forgetWalletOn(tx *sql.Tx, in string, args []any) error {
	for _, q := range []string{
		`DELETE FROM balance_tx WHERE user_id IN (` + in + `)`,
		`DELETE FROM promo_uses WHERE user_id IN (` + in + `)`,
		`UPDATE users SET referrer_id = 0 WHERE referrer_id IN (` + in + `)`,
		`UPDATE tg_subscribers SET ref_user_id = 0 WHERE ref_user_id IN (` + in + `)`,
	} {
		if _, err := tx.Exec(q, args...); err != nil {
			return err
		}
	}
	return nil
}

// ---- refunds, and what the panel shows about referrals and promo codes ----

// ErrNotRefundable is an order that cannot be refunded: not a paid plan order, or
// refunded already.
var ErrNotRefundable = errors.New("order cannot be refunded")

// RefundOrder returns a paid plan order's price — the money and the balance part
// alike — to the user's balance, once. Returns the user and the amount credited.
func (s *Store) RefundOrder(orderID, now int64) (userID, kop int64, err error) {
	err = s.withTx(func(tx *sql.Tx) error {
		r, err := tx.Exec(
			`UPDATE payment_orders SET refunded_at = ?, refund_source = 'balance'
			 WHERE id = ? AND status = 'paid' AND kind = 'plan' AND refunded_at = 0`, now, orderID)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNotRefundable
		}
		var amountRub int
		var balanceKop int64
		if err := tx.QueryRow(`SELECT user_id, amount_rub, balance_kop FROM payment_orders WHERE id = ?`, orderID).
			Scan(&userID, &amountRub, &balanceKop); err != nil {
			return err
		}
		kop = int64(amountRub)*100 + balanceKop
		if kop <= 0 {
			return ErrNotRefundable
		}
		ok, err := credit(tx, userID, kop, txEntry{Kind: model.TxRefund, OrderID: orderID, Now: now})
		if err == nil && !ok {
			err = sql.ErrNoRows
		}
		return err
	})
	return userID, kop, err
}

// ProviderRefund is what a refund by the payment system took back.
type ProviderRefund struct {
	UserID      int64
	Kind        string // the order's kind
	Earlier     string // its refund source before: "" or model.RefundToBalance
	ReturnedKop int64  // the balance part of a plan order, put back on the balance
	TakenKop    int64  // taken off the user's balance
	// ShortKop is what should have come off the user's balance but was spent already
	// — the operator's to settle.
	ShortKop    int64
	ReferrerID  int64
	RefTakenKop int64 // the referral credit taken back from the referrer
	RefShortKop int64 // the part of it the referrer had spent already
	RefDays     int   // bonus days taken back from the referrer
}

// ProviderRefundOrder records that the payment system returned a paid order's money
// (a refund, or a chargeback), once, and settles the balances in the same
// transaction:
//   - a top-up, or an order the operator had already refunded to the balance: what
//     that money put on the balance comes off it, as far as the balance goes;
//   - a plan order: the part the balance paid goes back onto it — the caller takes
//     the plan's time back, and that part was the user's own money;
//   - the referral credit the money earned comes off the referrer's balance.
//
// ErrNotRefundable when the order is not paid or was refunded by the provider before.
func (s *Store) ProviderRefundOrder(orderID, now int64) (ProviderRefund, error) {
	var r ProviderRefund
	err := s.withTx(func(tx *sql.Tx) error {
		var amountRub, refDays int
		var balanceKop int64
		err := tx.QueryRow(
			`SELECT user_id, kind, amount_rub, balance_kop, refund_source, ref_days FROM payment_orders
			 WHERE id = ? AND status = 'paid'`, orderID,
		).Scan(&r.UserID, &r.Kind, &amountRub, &balanceKop, &r.Earlier, &refDays)
		if errors.Is(err, sql.ErrNoRows) || r.Earlier == model.RefundByProvider {
			return ErrNotRefundable
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE payment_orders SET refund_source = 'provider',
			     refunded_at = CASE WHEN refunded_at = 0 THEN ? ELSE refunded_at END
			 WHERE id = ?`, now, orderID); err != nil {
			return err
		}
		money := int64(amountRub) * 100
		back := txEntry{Kind: model.TxChargeback, OrderID: orderID, Now: now}
		switch {
		case r.Earlier == model.RefundToBalance || r.Kind == model.OrderTopup:
			if r.TakenKop, err = debitUpTo(tx, r.UserID, money, back); err != nil {
				return err
			}
			r.ShortKop = money - r.TakenKop
		case balanceKop > 0:
			if _, err := credit(tx, r.UserID, balanceKop,
				txEntry{Kind: model.TxRefund, OrderID: orderID, Now: now}); err != nil {
				return err
			}
			r.ReturnedKop = balanceKop
		}
		// A negative referral line, so what the referrer "earned" nets it out.
		var earned int64
		if err := tx.QueryRow(
			`SELECT user_id, COALESCE(sum(amount_kop), 0) FROM balance_tx
			 WHERE kind = 'referral' AND order_id = ? GROUP BY user_id`, orderID,
		).Scan(&r.ReferrerID, &earned); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if earned > 0 {
			r.RefTakenKop, err = debitUpTo(tx, r.ReferrerID, earned,
				txEntry{Kind: model.TxReferral, OrderID: orderID, RefUserID: r.UserID, Now: now})
			if err != nil {
				return err
			}
			r.RefShortKop = earned - r.RefTakenKop
		}
		// Bonus days the payment gave the referrer: off what is still banked first, the
		// rest off the term they went onto.
		if refDays > 0 {
			if err := tx.QueryRow(`SELECT referrer_id FROM users WHERE id = ?`, r.UserID).Scan(&r.ReferrerID); err != nil {
				return err
			}
			if r.ReferrerID != 0 {
				var banked int
				if err := tx.QueryRow(`SELECT ref_bonus_days FROM users WHERE id = ?`, r.ReferrerID).Scan(&banked); err != nil &&
					!errors.Is(err, sql.ErrNoRows) {
					return err
				}
				fromBank := min(banked, refDays)
				if _, err := tx.Exec(
					`UPDATE users SET ref_bonus_days = ref_bonus_days - ?,
					     expire_at = CASE WHEN ? > 0 AND expire_at > ? THEN max(expire_at - ?, ?) ELSE expire_at END
					 WHERE id = ?`,
					fromBank, refDays-fromBank, now, int64(refDays-fromBank)*86400, now, r.ReferrerID); err != nil {
					return err
				}
				r.RefDays = refDays
			}
		}
		return nil
	})
	return r, err
}

// debitUpTo takes up to kop off the balance — all of it, or what there is — and
// returns what it took.
func debitUpTo(tx *sql.Tx, userID, kop int64, e txEntry) (int64, error) {
	var bal int64
	if err := tx.QueryRow(`SELECT balance_kop FROM users WHERE id = ?`, userID).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	take := min(bal, kop)
	if take <= 0 {
		return 0, nil
	}
	if _, err := debit(tx, userID, take, e); err != nil {
		return 0, err
	}
	return take, nil
}

// ShortenTerm takes secs off the term of a user still on planID with a running term,
// never below now, and returns the new end (0 = nothing to shorten).
func (s *Store) ShortenTerm(userID, planID, secs, now int64) (int64, error) {
	var exp int64
	err := s.db.QueryRow(
		`UPDATE users SET expire_at = max(expire_at - ?, ?)
		 WHERE id = ? AND plan_id = ? AND expire_at > ? RETURNING expire_at`,
		secs, now, userID, planID, now).Scan(&exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return exp, err
}

// ListReferrals returns the users someone invited, newest first, with what they paid
// and what that earned the inviter.
func (s *Store) ListReferrals(referrerID int64, limit int) ([]model.Referral, error) {
	rows, err := s.rdb.Query(
		`SELECT u.id, u.name, u.created_at,
		        (SELECT COALESCE(sum(amount_rub), 0) FROM payment_orders
		         WHERE user_id = u.id AND status = 'paid' AND refund_source <> 'provider'),
		        (SELECT COALESCE(sum(amount_kop), 0) FROM balance_tx
		         WHERE user_id = ? AND kind = 'referral' AND ref_user_id = u.id)
		 FROM users u WHERE u.referrer_id = ? AND u.referrer_id <> 0 ORDER BY u.id DESC LIMIT ?`,
		referrerID, referrerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Referral{}
	for rows.Next() {
		var r model.Referral
		var created sql.NullInt64
		if err := rows.Scan(&r.UserID, &r.Name, &created, &r.PaidRub, &r.EarnedKop); err != nil {
			return nil, err
		}
		r.CreatedAt = created.Int64
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPromoUses returns who used a code, newest first, with the order it discounted.
func (s *Store) ListPromoUses(promoID int64, limit int) ([]model.PromoUse, error) {
	rows, err := s.rdb.Query(
		`SELECT pu.user_id, COALESCE(u.name, ''), pu.order_id, pu.used_at,
		        COALESCE(o.amount_rub, 0), COALESCE(o.discount_rub, 0)
		 FROM promo_uses pu
		 LEFT JOIN users u ON u.id = pu.user_id
		 LEFT JOIN payment_orders o ON o.id = pu.order_id
		 WHERE pu.promo_id = ? ORDER BY pu.used_at DESC LIMIT ?`, promoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.PromoUse{}
	for rows.Next() {
		var u model.PromoUse
		if err := rows.Scan(&u.UserID, &u.Name, &u.OrderID, &u.UsedAt, &u.AmountRub, &u.DiscountRub); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// PromoRevenue is the money brought by paid orders a code discounted. Only plan
// orders: one that went to the balance instead kept its promo_id, but the code was
// never used on it.
func (s *Store) PromoRevenue(promoID int64) (orders, rub int, err error) {
	err = s.rdb.QueryRow(
		`SELECT count(*), COALESCE(sum(amount_rub), 0) FROM payment_orders
		 WHERE promo_id = ? AND status = 'paid' AND kind = 'plan' AND refund_source <> 'provider'`,
		promoID).Scan(&orders, &rub)
	return orders, rub, err
}

// ReferralStats sums up the referral programme and ranks the inviters.
func (s *Store) ReferralStats(top int) (model.ReferralStats, error) {
	var st model.ReferralStats
	err := s.rdb.QueryRow(
		`SELECT
		   (SELECT count(*) FROM users WHERE referrer_id <> 0),
		   (SELECT count(DISTINCT o.user_id) FROM users u CROSS JOIN payment_orders o ON o.user_id = u.id
		    WHERE u.referrer_id <> 0 AND o.status = 'paid' AND o.amount_rub > 0),
		   (SELECT COALESCE(sum(o.amount_rub), 0) FROM users u CROSS JOIN payment_orders o ON o.user_id = u.id
		    WHERE u.referrer_id <> 0 AND o.status = 'paid' AND o.refund_source <> 'provider'),
		   (SELECT COALESCE(sum(amount_kop), 0) FROM balance_tx WHERE kind = 'referral')`,
	).Scan(&st.Invited, &st.Paying, &st.RevenueRub, &st.PaidOutKop)
	if err != nil {
		return st, err
	}
	rows, err := s.rdb.Query(
		`SELECT r.id, r.name, count(*) AS invited,
		        (SELECT count(DISTINCT o.user_id) FROM users x CROSS JOIN payment_orders o ON o.user_id = x.id
		         WHERE x.referrer_id = r.id AND x.referrer_id <> 0 AND o.status = 'paid' AND o.amount_rub > 0),
		        (SELECT COALESCE(sum(amount_kop), 0) FROM balance_tx WHERE user_id = r.id AND kind = 'referral')
		 FROM users u JOIN users r ON r.id = u.referrer_id
		 WHERE u.referrer_id <> 0
		 GROUP BY r.id ORDER BY invited DESC, r.id LIMIT ?`, top)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	st.Top = []model.Referrer{}
	for rows.Next() {
		var r model.Referrer
		if err := rows.Scan(&r.UserID, &r.Name, &r.Invited, &r.Paying, &r.EarnedKop); err != nil {
			return st, err
		}
		st.Top = append(st.Top, r)
	}
	return st, rows.Err()
}
