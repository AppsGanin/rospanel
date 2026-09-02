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
	Search   string // free-text substring over action/target/actor/ip/details
	Since    int64  // created_at >= Since (unix seconds), 0 = no lower bound
	Until    int64  // created_at <= Until (unix seconds), 0 = no upper bound
	BeforeID int64  // page backwards from this id
	Limit    int
}

// adminAuditWhere renders the shared WHERE tail (everything after "WHERE 1 = 1") for
// both the paged list and the streaming export, so a filter can never mean one thing
// in the journal view and another in the exported file.
func adminAuditWhere(f AdminAuditFilter) (string, []any) {
	var q strings.Builder
	var args []any
	if len(f.Actions) > 0 {
		q.WriteString(` AND action IN (?` + strings.Repeat(`, ?`, len(f.Actions)-1) + `)`)
		for _, a := range f.Actions {
			args = append(args, a)
		}
	}
	if f.Actor != "" {
		q.WriteString(` AND actor_name = ?`)
		args = append(args, f.Actor)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		// Literal substring: escape LIKE's own metacharacters so a search for "50%"
		// or "user_1" matches those exact strings rather than acting as wildcards.
		pat := "%" + likeEscape(s) + "%"
		q.WriteString(` AND (action LIKE ? ESCAPE '\' OR target LIKE ? ESCAPE '\'` +
			` OR actor_name LIKE ? ESCAPE '\' OR ip LIKE ? ESCAPE '\'` +
			` OR details LIKE ? ESCAPE '\')`)
		args = append(args, pat, pat, pat, pat, pat)
	}
	if f.Since > 0 {
		q.WriteString(` AND created_at >= ?`)
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		q.WriteString(` AND created_at <= ?`)
		args = append(args, f.Until)
	}
	if f.BeforeID > 0 {
		q.WriteString(` AND id < ?`)
		args = append(args, f.BeforeID)
	}
	return q.String(), args
}

// likeEscape backslash-escapes the LIKE metacharacters so a user's search term is
// matched literally (paired with `ESCAPE '\'` in the query).
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// scanAdminAudit reads one trail row. A row whose details are corrupt still comes
// back, with nil details, rather than failing the whole page/export.
func scanAdminAudit(rows interface{ Scan(...any) error }) (model.AdminAudit, error) {
	var ev model.AdminAudit
	var raw string
	if err := rows.Scan(&ev.ID, &ev.Action, &ev.Target, &ev.ActorKind,
		&ev.ActorName, &ev.IP, &raw, &ev.CreatedAt); err != nil {
		return ev, err
	}
	if raw != "" {
		var d any
		if json.Unmarshal([]byte(raw), &d) == nil {
			ev.Details = d
		}
	}
	return ev, nil
}

// ListAdminAudit returns the admin trail, newest first, filtered per f.
func (s *Store) ListAdminAudit(f AdminAuditFilter) ([]model.AdminAudit, error) {
	where, args := adminAuditWhere(f)
	q := `SELECT ` + adminAuditCols + ` FROM admin_audit WHERE 1 = 1` + where +
		` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.AdminAudit
	for rows.Next() {
		ev, err := scanAdminAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// StreamAdminAudit walks the filtered trail newest-first, invoking fn per row without
// buffering the whole result set — the export writes each row straight to the client.
// f.BeforeID is honoured like any other filter; f.Limit caps the walk.
func (s *Store) StreamAdminAudit(f AdminAuditFilter, fn func(model.AdminAudit) error) error {
	where, args := adminAuditWhere(f)
	q := `SELECT ` + adminAuditCols + ` FROM admin_audit WHERE 1 = 1` + where +
		` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		ev, err := scanAdminAudit(rows)
		if err != nil {
			return err
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	return rows.Err()
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

// AdminLoginSeenFrom reports whether this admin has signed in from this address
// since `since` — the memory behind the "new address" alert. The audit trail is
// the record rather than the session table: sessions are deleted on logout and
// expiry, and an address used every morning would read as new every evening.
func (s *Store) AdminLoginSeenFrom(username, ip string, since int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_audit
		WHERE action = ? AND actor_name = ? AND ip = ? AND created_at >= ?`,
		model.AuditLogin, username, ip, since).Scan(&n)
	return n > 0, err
}
