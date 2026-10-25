package store

import (
	"database/sql"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

const alertRuleCols = `id, name, enabled, metric, op, threshold, for_secs, node_id, created_at`

func scanAlertRules(rows *sql.Rows) ([]model.AlertRule, error) {
	defer rows.Close()
	var out []model.AlertRule
	for rows.Next() {
		var r model.AlertRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &enabled, &r.Metric, &r.Op, &r.Threshold,
			&r.ForSecs, &r.NodeID, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAlertRules returns every rule in creation order, each with the number of
// firings still open.
func (s *Store) ListAlertRules() ([]model.AlertRule, error) {
	rows, err := s.db.Query(`SELECT ` + alertRuleCols + ` FROM alert_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	rules, err := scanAlertRules(rows)
	if err != nil || len(rules) == 0 {
		return rules, err
	}
	open, err := s.db.Query(`SELECT rule_id, COUNT(*) FROM alert_events WHERE resolved_at = 0 GROUP BY rule_id`)
	if err != nil {
		return nil, err
	}
	defer open.Close()
	firing := map[int64]int{}
	for open.Next() {
		var id int64
		var n int
		if err := open.Scan(&id, &n); err != nil {
			return nil, err
		}
		firing[id] = n
	}
	for i := range rules {
		rules[i].Firing = firing[rules[i].ID]
	}
	return rules, open.Err()
}

// GetAlertRule returns one rule, sql.ErrNoRows when there is none.
func (s *Store) GetAlertRule(id int64) (*model.AlertRule, error) {
	rows, err := s.db.Query(`SELECT `+alertRuleCols+` FROM alert_rules WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	rules, err := scanAlertRules(rows)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, sql.ErrNoRows
	}
	return &rules[0], nil
}

// CountAlertRules is how many rules exist.
func (s *Store) CountAlertRules() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM alert_rules`).Scan(&n)
	return n, err
}

// CreateAlertRule stores a rule.
func (s *Store) CreateAlertRule(in model.AlertRuleInput) (*model.AlertRule, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO alert_rules (name, enabled, metric, op, threshold, for_secs, node_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Name, boolToInt(in.Enabled), in.Metric, in.Op, in.Threshold, in.ForSecs, in.NodeID, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.AlertRule{ID: id, AlertRuleInput: in, CreatedAt: now}, nil
}

// UpdateAlertRule replaces a rule's settings; sql.ErrNoRows when there is none.
func (s *Store) UpdateAlertRule(id int64, in model.AlertRuleInput) error {
	res, err := s.db.Exec(
		`UPDATE alert_rules SET name = ?, enabled = ?, metric = ?, op = ?, threshold = ?,
		        for_secs = ?, node_id = ? WHERE id = ?`,
		in.Name, boolToInt(in.Enabled), in.Metric, in.Op, in.Threshold, in.ForSecs, in.NodeID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteAlertRule removes a rule and closes its open firings at `at`; the finished
// history stays.
func (s *Store) DeleteAlertRule(id, at int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE alert_events SET resolved_at = ? WHERE rule_id = ? AND resolved_at = 0`, at, id); err != nil {
			return err
		}
		res, err := tx.Exec(`DELETE FROM alert_rules WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

const alertEventCols = `id, rule_id, rule_name, node_id, node_name, metric, op, threshold,
	value, peak, fired_at, resolved_at`

func scanAlertEvents(rows *sql.Rows) ([]model.AlertEvent, error) {
	defer rows.Close()
	var out []model.AlertEvent
	for rows.Next() {
		var e model.AlertEvent
		if err := rows.Scan(&e.ID, &e.RuleID, &e.RuleName, &e.NodeID, &e.NodeName, &e.Metric,
			&e.Op, &e.Threshold, &e.Value, &e.Peak, &e.FiredAt, &e.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddAlertEvent opens a firing and returns its id.
func (s *Store) AddAlertEvent(e model.AlertEvent) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO alert_events (rule_id, rule_name, node_id, node_name, metric, op, threshold,
		                           value, peak, fired_at, resolved_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		e.RuleID, e.RuleName, e.NodeID, e.NodeName, e.Metric, e.Op, e.Threshold, e.Value, e.Peak, e.FiredAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetAlertEventPeak records a worse figure for an open firing.
func (s *Store) SetAlertEventPeak(id int64, peak float64) error {
	_, err := s.db.Exec(`UPDATE alert_events SET peak = ? WHERE id = ? AND resolved_at = 0`, peak, id)
	return err
}

// ResolveAlertEvent closes a firing.
func (s *Store) ResolveAlertEvent(id, at int64) error {
	_, err := s.db.Exec(`UPDATE alert_events SET resolved_at = ? WHERE id = ? AND resolved_at = 0`, at, id)
	return err
}

// ResolveAlertEventsOfRule closes every open firing of a rule, for a rule that was
// switched off or changed so the old firing no longer describes it.
func (s *Store) ResolveAlertEventsOfRule(ruleID, at int64) error {
	_, err := s.db.Exec(`UPDATE alert_events SET resolved_at = ? WHERE rule_id = ? AND resolved_at = 0`, at, ruleID)
	return err
}

// OpenAlertEvents lists the firings that have not cleared.
func (s *Store) OpenAlertEvents() ([]model.AlertEvent, error) {
	rows, err := s.db.Query(`SELECT ` + alertEventCols + ` FROM alert_events WHERE resolved_at = 0`)
	if err != nil {
		return nil, err
	}
	return scanAlertEvents(rows)
}

// ListAlertEvents pages the history, newest first; beforeID 0 starts at the top.
func (s *Store) ListAlertEvents(limit int, beforeID int64) ([]model.AlertEvent, error) {
	q := `SELECT ` + alertEventCols + ` FROM alert_events`
	var args []any
	if beforeID > 0 {
		q += ` WHERE id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanAlertEvents(rows)
}

// PurgeAlertEvents drops finished firings that cleared before cutoff.
func (s *Store) PurgeAlertEvents(cutoff int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM alert_events WHERE resolved_at > 0 AND resolved_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
