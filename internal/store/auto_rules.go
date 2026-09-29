package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

const autoRuleCols = `id, name, enabled, trigger, delay_hours, text, buttons,
	discount_percent, discount_days, created_at, updated_at`

func scanAutoRule(sc interface{ Scan(...any) error }) (model.AutoRule, error) {
	var r model.AutoRule
	var enabled int
	var buttons string
	err := sc.Scan(&r.ID, &r.Name, &enabled, &r.Trigger, &r.DelayHours, &r.Text, &buttons,
		&r.DiscountPercent, &r.DiscountDays, &r.CreatedAt, &r.UpdatedAt)
	r.Enabled = enabled != 0
	if buttons != "" {
		_ = json.Unmarshal([]byte(buttons), &r.Buttons)
	}
	if r.Buttons == nil {
		r.Buttons = []model.BroadcastButton{}
	}
	return r, err
}

// ListAutoRules returns every rule, oldest first, with what each did.
func (s *Store) ListAutoRules() ([]model.AutoRule, error) {
	rows, err := s.db.Query(`SELECT ` + autoRuleCols + ` FROM auto_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []model.AutoRule
	for rows.Next() {
		r, err := scanAutoRule(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Stats, err = s.autoRuleStats(out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetAutoRule reads one rule.
func (s *Store) GetAutoRule(id int64) (model.AutoRule, error) {
	return scanAutoRule(s.db.QueryRow(`SELECT `+autoRuleCols+` FROM auto_rules WHERE id = ?`, id))
}

// SaveAutoRule creates the rule (ID 0) or updates it.
func (s *Store) SaveAutoRule(r *model.AutoRule, now int64) error {
	buttons, _ := json.Marshal(r.Buttons)
	if r.ID == 0 {
		r.CreatedAt, r.UpdatedAt = now, now
		return s.db.QueryRow(
			`INSERT INTO auto_rules (name, enabled, trigger, delay_hours, text, buttons,
			     discount_percent, discount_days, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			r.Name, boolToInt(r.Enabled), r.Trigger, r.DelayHours, r.Text, string(buttons),
			r.DiscountPercent, r.DiscountDays, now, now).Scan(&r.ID)
	}
	r.UpdatedAt = now
	res, err := s.db.Exec(
		`UPDATE auto_rules SET name = ?, enabled = ?, trigger = ?, delay_hours = ?, text = ?, buttons = ?,
		     discount_percent = ?, discount_days = ?, updated_at = ? WHERE id = ?`,
		r.Name, boolToInt(r.Enabled), r.Trigger, r.DelayHours, r.Text, string(buttons),
		r.DiscountPercent, r.DiscountDays, now, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteAutoRule drops a rule and its send log. Codes it already gave out stay valid.
func (s *Store) DeleteAutoRule(id int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM auto_rule_sends WHERE rule_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM auto_rules WHERE id = ?`, id)
		return err
	})
}

// AutoRuleTarget is someone a rule is due to write to.
type AutoRuleTarget struct {
	UserID int64 // 0 = a chat with no account
	ChatID int64
	Cycle  int64
	Name   string
}

// AutoRuleTargets returns who reached the rule's trigger in (floor, cut] and has not
// had this rule's message for that cycle. Only chats that still take messages: not
// blocked, not unsubscribed. It walks users and subscribers, so it reads through the
// writer (the read pool is for bounded lookups).
func (s *Store) AutoRuleTargets(r model.AutoRule, floor, cut, now int64, limit int) ([]AutoRuleTarget, error) {
	const reachable = ` s.active = 1 AND s.opt_out = 0 `
	const notSent = ` NOT EXISTS (SELECT 1 FROM auto_rule_sends x WHERE x.rule_id = ? AND x.chat_id = s.chat_id AND x.cycle = %s) `
	userBase := `FROM users u JOIN tg_subscribers s ON s.chat_id = u.tg_chat_id
		WHERE u.tg_chat_id <> 0 AND u.enabled = 1 AND` + reachable
	neverPaid := ` NOT EXISTS (SELECT 1 FROM payment_orders o WHERE o.user_id = u.id AND o.status = 'paid' AND o.amount_rub > 0) `
	var q string
	var args []any
	switch r.Trigger {
	case model.TriggerNoSignup:
		q = `SELECT 0, s.chat_id, 0, s.first_name FROM tg_subscribers s
			WHERE` + reachable + `AND s.started_at > ? AND s.started_at <= ?
			  AND NOT EXISTS (SELECT 1 FROM users u WHERE u.tg_chat_id = s.chat_id AND u.tg_chat_id <> 0)
			  AND NOT EXISTS (SELECT 1 FROM registration_requests q WHERE q.chat_id = s.chat_id)
			  AND` + strings.Replace(notSent, "%s", "0", 1)
		args = []any{floor, cut, r.ID}
	case model.TriggerNoConnect:
		q = `SELECT u.id, s.chat_id, 0, u.name ` + userBase + `AND u.last_seen = 0
			  AND u.created_at > ? AND u.created_at <= ? AND` + strings.Replace(notSent, "%s", "0", 1)
		args = []any{floor, cut, r.ID}
	case model.TriggerTrialNoPay:
		q = `SELECT u.id, s.chat_id, 0, u.name ` + userBase + `AND u.trial_used <> 0 AND` + neverPaid + `
			  AND u.created_at > ? AND u.created_at <= ? AND` + strings.Replace(notSent, "%s", "0", 1)
		args = []any{floor, cut, r.ID}
	case model.TriggerIdle:
		q = `SELECT u.id, s.chat_id, u.last_seen, u.name ` + userBase + `AND u.last_seen > ? AND u.last_seen <= ?
			  AND (u.expire_at = 0 OR u.expire_at > ?) AND` + strings.Replace(notSent, "%s", "u.last_seen", 1)
		args = []any{floor, cut, now, r.ID}
	case model.TriggerLapsed:
		q = `SELECT id, chat_id, lapse, name FROM (
			   SELECT u.id, s.chat_id, u.name,
			          max(u.lapsed_at, CASE WHEN u.expire_at > 0 AND u.expire_at <= ? THEN u.expire_at ELSE 0 END) AS lapse
			   ` + userBase + `
			     AND EXISTS (SELECT 1 FROM payment_orders o WHERE o.user_id = u.id AND o.status = 'paid'
			                 AND o.kind = 'plan' AND o.refunded_at = 0)
			     AND NOT EXISTS (SELECT 1 FROM payment_orders o WHERE o.user_id = u.id AND o.refund_source = 'provider')
			 ) t
			 WHERE t.lapse > ? AND t.lapse <= ?
			   AND NOT EXISTS (SELECT 1 FROM auto_rule_sends x WHERE x.rule_id = ? AND x.chat_id = t.chat_id AND x.cycle = t.lapse)`
		args = []any{now, floor, cut, r.ID}
	default:
		return nil, nil
	}
	q += ` LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutoRuleTarget
	for rows.Next() {
		var t AutoRuleTarget
		if err := rows.Scan(&t.UserID, &t.ChatID, &t.Cycle, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ErrAutoRuleSent is a message this rule already sent for that cycle.
var ErrAutoRuleSent = errors.New("the rule already wrote to this chat for this cycle")

// RecordAutoRuleSend claims one message: false when it was already sent.
func (s *Store) RecordAutoRuleSend(ruleID int64, t AutoRuleTarget, now int64) (bool, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO auto_rule_sends (rule_id, chat_id, cycle, user_id, sent_at) VALUES (?, ?, ?, ?, ?)`,
		ruleID, t.ChatID, t.Cycle, t.UserID, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// IssueAutoRuleCode claims the message and mints its personal code in one
// transaction, attaching the code to the user's next payment when no other live code
// is attached. ErrAutoRuleSent when the message went out already.
func (s *Store) IssueAutoRuleCode(ruleID int64, t AutoRuleTarget, p *model.PromoCode, code func() string, now int64) (attached bool, err error) {
	err = s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`INSERT OR IGNORE INTO auto_rule_sends (rule_id, chat_id, cycle, user_id, sent_at) VALUES (?, ?, ?, ?, ?)`,
			ruleID, t.ChatID, t.Cycle, t.UserID, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrAutoRuleSent
		}
		for range 5 {
			p.Code = code()
			err = tx.QueryRow(
				`INSERT INTO promo_codes (code, kind, value, max_uses, expires_at, enabled, note,
				     created_at, winback_user, auto_rule)
				 VALUES (?, ?, ?, 1, ?, 1, ?, ?, ?, ?) RETURNING id`,
				p.Code, p.Kind, p.Value, p.ExpiresAt, p.Note, now, t.UserID, ruleID,
			).Scan(&p.ID)
			if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
				break
			}
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE auto_rule_sends SET promo_id = ? WHERE rule_id = ? AND chat_id = ? AND cycle = ?`,
			p.ID, ruleID, t.ChatID, t.Cycle); err != nil {
			return err
		}
		r, err := tx.Exec(
			`UPDATE users SET promo_id = ?
			 WHERE id = ? AND (promo_id = 0 OR promo_id IN (
			     SELECT id FROM promo_codes WHERE winback_user <> 0
			       AND ((expires_at > 0 AND expires_at <= ?) OR uses >= max_uses)))`,
			p.ID, t.UserID, now)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		attached = n > 0
		return nil
	})
	return attached, err
}

// autoRuleStats counts what one rule did.
func (s *Store) autoRuleStats(r model.AutoRule) (model.AutoRuleStats, error) {
	var st model.AutoRuleStats
	window := int64(model.AutoRuleConvertDays) * 86400
	convert := `EXISTS (SELECT 1 FROM payment_orders o WHERE o.user_id = x.user_id AND o.status = 'paid'
	                    AND o.amount_rub > 0 AND o.refund_source <> 'provider'
	                    AND o.paid_at > x.sent_at AND o.paid_at <= x.sent_at + ?1)`
	if r.Trigger == model.TriggerNoSignup {
		convert = `(?1 > 0 AND EXISTS (SELECT 1 FROM users u WHERE u.tg_chat_id = x.chat_id AND u.tg_chat_id <> 0))`
	}
	err := s.db.QueryRow(
		`SELECT count(*), COALESCE(sum(`+convert+`), 0),
		        COALESCE((SELECT sum(o.amount_rub) FROM auto_rule_sends y
		                  JOIN payment_orders o ON o.user_id = y.user_id AND y.user_id <> 0
		                  WHERE y.rule_id = ?2 AND o.status = 'paid' AND o.refund_source <> 'provider'
		                    AND o.paid_at > y.sent_at AND o.paid_at <= y.sent_at + ?1), 0),
		        COALESCE((SELECT sum(uses) FROM promo_codes WHERE auto_rule = ?2), 0)
		 FROM auto_rule_sends x WHERE x.rule_id = ?2`, window, r.ID,
	).Scan(&st.Sent, &st.Converted, &st.RevenueRub, &st.CodesUsed)
	return st, err
}
