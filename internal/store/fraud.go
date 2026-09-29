package store

import (
	"database/sql"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Fraud signals: patterns in data the panel already keeps that are worth an
// operator's look. Nothing here decides or blocks — each query names the accounts
// and the thing they share, capped, newest data only.

// fraudRowCap bounds each signal's groups: the page is a list to look through.
const fraudRowCap = 30

// FraudWindow is how far back the signals look.
type FraudWindow struct {
	Since     int64 // connections, devices, promo uses, refunds (30 days)
	WeekSince int64 // failed payments (7 days)
}

// FraudSignals computes every signal.
func (s *Store) FraudSignals(w FraudWindow) ([]model.FraudSignal, error) {
	var out []model.FraudSignal
	add := func(kind, q string, args ...any) error {
		sigs, err := s.fraudGroups(kind, q, args...)
		out = append(out, sigs...)
		return err
	}
	// Trial farming: one address that several trial users who never paid connect from.
	if err := add(model.FraudTrialFarm,
		`SELECT c.ip, count(DISTINCT u.id), max(c.last_seen), group_concat(DISTINCT u.id)
		 FROM connections c JOIN users u ON u.id = c.user_id
		 WHERE c.last_seen >= ? AND u.trial_used <> 0
		   AND NOT EXISTS (SELECT 1 FROM payment_orders o WHERE o.user_id = u.id AND o.status = 'paid' AND o.amount_rub > 0)
		 GROUP BY c.ip HAVING count(DISTINCT u.id) >= ?
		 ORDER BY count(DISTINCT u.id) DESC LIMIT ?`, w.Since, model.FraudTrialFarmMin, fraudRowCap); err != nil {
		return nil, err
	}
	// One device on several accounts.
	if err := add(model.FraudSharedDevice,
		`SELECT hwid, count(DISTINCT user_id), max(last_seen), group_concat(DISTINCT user_id)
		 FROM devices WHERE last_seen >= ?
		 GROUP BY hwid HAVING count(DISTINCT user_id) >= 2
		 ORDER BY count(DISTINCT user_id) DESC, max(last_seen) DESC LIMIT ?`, w.Since, fraudRowCap); err != nil {
		return nil, err
	}
	// Inviting oneself: the invitee shares a device, or an address, with the referrer.
	if err := add(model.FraudSelfReferral,
		`SELECT key, 2, at, ids FROM (
		   SELECT 'hwid:' || a.hwid AS key, max(a.last_seen, b.last_seen) AS at, u.referrer_id || ',' || u.id AS ids
		   FROM users u
		   JOIN devices a ON a.user_id = u.id AND a.last_seen >= ?1
		   JOIN devices b ON b.user_id = u.referrer_id AND b.hwid = a.hwid
		   WHERE u.referrer_id <> 0
		   UNION
		   SELECT 'ip:' || a.ip, max(a.last_seen, b.last_seen), u.referrer_id || ',' || u.id
		   FROM users u
		   JOIN connections a ON a.user_id = u.id AND a.last_seen >= ?1
		   JOIN connections b ON b.user_id = u.referrer_id AND b.ip = a.ip AND b.last_seen >= ?1
		   WHERE u.referrer_id <> 0
		 ) GROUP BY ids ORDER BY at DESC LIMIT ?2`, w.Since, fraudRowCap); err != nil {
		return nil, err
	}
	// A promo code taken many times within ten minutes.
	if err := add(model.FraudPromoBurst,
		`SELECT pc.code, count(*), max(pu.used_at), group_concat(pu.user_id)
		 FROM promo_uses pu JOIN promo_codes pc ON pc.id = pu.promo_id
		 WHERE pu.used_at >= ?
		 GROUP BY pu.promo_id, pu.used_at / 600 HAVING count(*) >= ?
		 ORDER BY max(pu.used_at) DESC LIMIT ?`, w.Since, model.FraudPromoBurstMin, fraudRowCap); err != nil {
		return nil, err
	}
	// Payment attempts that keep failing: a card being tried.
	if err := add(model.FraudFailedPayments,
		`SELECT user_id, count(*), max(created_at), user_id
		 FROM payment_orders
		 WHERE status = 'cancelled' AND provider NOT IN ('', 'balance') AND created_at >= ?
		 GROUP BY user_id HAVING count(*) >= ?
		 ORDER BY count(*) DESC LIMIT ?`, w.WeekSince, model.FraudFailedMin, fraudRowCap); err != nil {
		return nil, err
	}
	// Many payments within an hour.
	if err := add(model.FraudPaymentBurst,
		`SELECT user_id, count(*), max(paid_at), user_id
		 FROM payment_orders
		 WHERE status = 'paid' AND amount_rub > 0 AND paid_at >= ?
		 GROUP BY user_id, paid_at / 3600 HAVING count(*) >= ?
		 ORDER BY max(paid_at) DESC LIMIT ?`, w.Since, model.FraudPaymentBurstMin, fraudRowCap); err != nil {
		return nil, err
	}
	// Money taken back through the payment system.
	if err := add(model.FraudRefunds,
		`SELECT user_id, count(*), max(refunded_at), user_id
		 FROM payment_orders
		 WHERE refund_source = 'provider' AND refunded_at >= ?
		 GROUP BY user_id HAVING count(*) >= ?
		 ORDER BY count(*) DESC LIMIT ?`, w.Since, model.FraudRefundsMin, fraudRowCap); err != nil {
		return nil, err
	}
	return out, s.nameFraudUsers(out)
}

// fraudGroups runs one signal query: each row is (key, count, last seen, user ids).
func (s *Store) fraudGroups(kind, q string, args ...any) ([]model.FraudSignal, error) {
	rows, err := s.rdb.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FraudSignal
	for rows.Next() {
		var key, ids sql.NullString
		var sig model.FraudSignal
		if err := rows.Scan(&key, &sig.Count, &sig.At, &ids); err != nil {
			return nil, err
		}
		sig.Kind, sig.Key = kind, key.String
		seen := map[int64]bool{}
		for _, part := range strings.Split(ids.String, ",") {
			if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && !seen[id] {
				seen[id] = true
				sig.Users = append(sig.Users, model.FraudUser{ID: id})
			}
		}
		out = append(out, sig)
	}
	return out, rows.Err()
}

// nameFraudUsers fills in the names, one lookup for every account the signals name.
func (s *Store) nameFraudUsers(sigs []model.FraudSignal) error {
	ids := map[int64]bool{}
	for _, sig := range sigs {
		for _, u := range sig.Users {
			ids[u.ID] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	ph := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for id := range ids {
		ph = append(ph, "?")
		args = append(args, id)
	}
	rows, err := s.rdb.Query(`SELECT id, name FROM users WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	names := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		names[id] = name
	}
	for i := range sigs {
		for j := range sigs[i].Users {
			sigs[i].Users[j].Name = names[sigs[i].Users[j].ID]
		}
	}
	return rows.Err()
}
