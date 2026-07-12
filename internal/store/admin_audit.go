package store

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

const adminAuditCols = `id, action, target, actor_kind, actor_name, ip, details, created_at`

// AddAdminAudit appends one row to the admin trail. Details that won't marshal are
// dropped rather than failing the write — a row with no details still beats losing
// the event.
func (s *Store) AddAdminAudit(ev model.AdminAudit) error {
	raw := ""
	if ev.Details != nil {
		if b, err := json.Marshal(ev.Details); err == nil {
			raw = string(b)
		}
	}
	if ev.CreatedAt == 0 {
		ev.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO admin_audit (action, target, actor_kind, actor_name, ip, details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ev.Action, ev.Target, ev.ActorKind, ev.ActorName, ev.IP, raw, ev.CreatedAt,
	)
	return err
}

// AdminAuditFilter narrows the trail. A zero field means "no filter".
type AdminAuditFilter struct {
	// Actions matches any one of these action keys — a category filter expands to the
	// keys in that category (model.AdminAuditActionsIn). Empty means every action.
	Actions  []string
	Actor    string // actor_name, exact
	BeforeID int64  // page backwards from this id
	Limit    int
}

// ListAdminAudit returns the admin trail, newest first, filtered per f.
func (s *Store) ListAdminAudit(f AdminAuditFilter) ([]model.AdminAudit, error) {
	q := `SELECT ` + adminAuditCols + ` FROM admin_audit WHERE 1 = 1`
	var args []any
	if len(f.Actions) > 0 {
		q += ` AND action IN (?` + strings.Repeat(`, ?`, len(f.Actions)-1) + `)`
		for _, a := range f.Actions {
			args = append(args, a)
		}
	}
	if f.Actor != "" {
		q += ` AND actor_name = ?`
		args = append(args, f.Actor)
	}
	if f.BeforeID > 0 {
		q += ` AND id < ?`
		args = append(args, f.BeforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.AdminAudit
	for rows.Next() {
		var ev model.AdminAudit
		var raw string
		if err := rows.Scan(&ev.ID, &ev.Action, &ev.Target, &ev.ActorKind,
			&ev.ActorName, &ev.IP, &raw, &ev.CreatedAt); err != nil {
			return nil, err
		}
		// A row whose details are corrupt still comes back, with nil details, rather
		// than failing the whole page.
		if raw != "" {
			var d any
			if json.Unmarshal([]byte(raw), &d) == nil {
				ev.Details = d
			}
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// PurgeAdminAudit drops trail rows older than the cutoff (unix seconds), returning
// how many were removed. Batched for the same reason as the user journal's sweep:
// the pool is a single connection, so an unbounded delete would stall every request
// behind it.
func (s *Store) PurgeAdminAudit(before int64) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(
			`DELETE FROM admin_audit WHERE id IN (
				SELECT id FROM admin_audit WHERE created_at < ? LIMIT ?
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
