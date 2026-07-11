package store

import (
	"encoding/json"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

const userEventCols = `id, user_id, user_name, action, actor_kind, actor_name, details, created_at`

// AddUserEvent appends one audit row. details may be nil (stored as ""); a value
// that won't marshal is dropped rather than failing the write — an audit row with
// no details still beats losing the event.
func (s *Store) AddUserEvent(ev model.UserEvent) error {
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
		`INSERT INTO user_events (user_id, user_name, action, actor_kind, actor_name, details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ev.UserID, ev.UserName, ev.Action, ev.ActorKind, ev.ActorName, raw, ev.CreatedAt,
	)
	return err
}

// scanUserEvents runs a query and decodes rows, unmarshalling the details JSON back
// into a generic value (a row whose details are corrupt still comes back, with nil
// details, rather than failing the whole page).
func (s *Store) scanUserEvents(query string, args ...any) ([]model.UserEvent, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.UserEvent
	for rows.Next() {
		var ev model.UserEvent
		var raw string
		if err := rows.Scan(&ev.ID, &ev.UserID, &ev.UserName, &ev.Action,
			&ev.ActorKind, &ev.ActorName, &raw, &ev.CreatedAt); err != nil {
			return nil, err
		}
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

// ListUserEvents returns one user's audit trail, newest first. beforeID > 0 pages
// backwards from a previously-returned id (the rows are id-ordered, so the id is a
// stable cursor even as new events land at the top).
func (s *Store) ListUserEvents(userID int64, limit int, beforeID int64) ([]model.UserEvent, error) {
	if beforeID > 0 {
		return s.scanUserEvents(
			`SELECT `+userEventCols+` FROM user_events
			 WHERE user_id = ? AND id < ? ORDER BY id DESC LIMIT ?`,
			userID, beforeID, limit)
	}
	return s.scanUserEvents(
		`SELECT `+userEventCols+` FROM user_events
		 WHERE user_id = ? ORDER BY id DESC LIMIT ?`,
		userID, limit)
}

// UserEventFilter narrows the global journal. A zero field means "no filter".
type UserEventFilter struct {
	Action    string
	ActorKind string
	UserID    int64
	BeforeID  int64 // page backwards from this id
	Limit     int
}

// ListEvents returns the global audit trail, newest first, filtered per f.
func (s *Store) ListEvents(f UserEventFilter) ([]model.UserEvent, error) {
	q := `SELECT ` + userEventCols + ` FROM user_events WHERE 1 = 1`
	var args []any
	if f.Action != "" {
		q += ` AND action = ?`
		args = append(args, f.Action)
	}
	if f.ActorKind != "" {
		q += ` AND actor_kind = ?`
		args = append(args, f.ActorKind)
	}
	if f.UserID > 0 {
		q += ` AND user_id = ?`
		args = append(args, f.UserID)
	}
	if f.BeforeID > 0 {
		q += ` AND id < ?`
		args = append(args, f.BeforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)
	return s.scanUserEvents(q, args...)
}

// purgeBatch bounds one DELETE of the retention sweep. The pool is a single
// connection (see store.Open), so an unbounded delete of a large backlog would hold
// it for the whole statement and stall every request behind it. Deleting in batches
// lets other queries interleave.
const purgeBatch = 500

// PurgeUserEvents drops audit rows older than the cutoff (unix seconds), returning
// how many were removed. This is the retention sweep.
func (s *Store) PurgeUserEvents(before int64) (int64, error) {
	var total int64
	for {
		res, err := s.db.Exec(
			`DELETE FROM user_events WHERE id IN (
				SELECT id FROM user_events WHERE created_at < ? LIMIT ?
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
