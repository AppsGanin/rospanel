package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Status page incidents and maintenance windows (migration 0127).

const incidentCols = `id, kind, title, body, node_ids, starts_at, ends_at, resolved_at, created_at, updated_at, hide_in_sub,
	compensated_at, compensated_seconds, compensated_users, snapshot_at`

func scanIncident(sc interface{ Scan(...any) error }) (model.Incident, error) {
	var (
		in    model.Incident
		nodes string
	)
	var hide int
	if err := sc.Scan(&in.ID, &in.Kind, &in.Title, &in.Body, &nodes, &in.StartsAt,
		&in.EndsAt, &in.ResolvedAt, &in.CreatedAt, &in.UpdatedAt, &hide,
		&in.CompensatedAt, &in.CompensatedSeconds, &in.CompensatedUsers, &in.SnapshotAt); err != nil {
		return in, err
	}
	in.HideInSub = hide != 0
	in.NodeIDs = []int64{}
	_ = json.Unmarshal([]byte(nodes), &in.NodeIDs)
	return in, nil
}

func incidentNodes(ids []int64) string {
	if ids == nil {
		ids = []int64{}
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

// ListIncidents returns up to limit incidents, the latest to start first, each with
// its course.
func (s *Store) ListIncidents(limit int) ([]model.Incident, error) {
	return s.queryIncidents(`SELECT `+incidentCols+` FROM incidents
		ORDER BY starts_at DESC, id DESC LIMIT ?`, limit)
}

// queryIncidents runs a select of incidentCols and fills in every row's updates.
func (s *Store) queryIncidents(query string, args ...any) ([]model.Incident, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	out := []model.Incident{}
	for rows.Next() {
		in, err := scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, in)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachIncidentUpdates(out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachIncidentUpdates loads the updates of the given incidents in one query.
func (s *Store) attachIncidentUpdates(list []model.Incident) error {
	if len(list) == 0 {
		return nil
	}
	at := make(map[int64]int, len(list))
	ids := make([]string, 0, len(list))
	for i := range list {
		list[i].Updates = []model.IncidentUpdate{}
		at[list[i].ID] = i
		ids = append(ids, strconv.FormatInt(list[i].ID, 10))
	}
	rows, err := s.db.Query(`SELECT id, incident_id, stage, body, created_at FROM incident_updates
		WHERE incident_id IN (` + strings.Join(ids, ",") + `) ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u model.IncidentUpdate
		if err := rows.Scan(&u.ID, &u.IncidentID, &u.Stage, &u.Body, &u.CreatedAt); err != nil {
			return err
		}
		if i, ok := at[u.IncidentID]; ok {
			list[i].Updates = append(list[i].Updates, u)
		}
	}
	return rows.Err()
}

// IncidentsForPage returns what the status page can show at now: everything not
// over yet, and what ended since `since`.
func (s *Store) IncidentsForPage(now, since int64) ([]model.Incident, error) {
	return s.queryIncidents(`SELECT `+incidentCols+` FROM incidents
		WHERE (resolved_at = 0 AND (kind <> 'maintenance' OR ends_at = 0 OR ends_at > ?))
		   OR resolved_at >= ? OR (resolved_at = 0 AND ends_at >= ?)
		ORDER BY starts_at DESC, id DESC`, now, since, since)
}

// GetIncident returns one incident, or nil.
func (s *Store) GetIncident(id int64) (*model.Incident, error) {
	in, err := scanIncident(s.db.QueryRow(`SELECT `+incidentCols+` FROM incidents WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	list := []model.Incident{in}
	if err := s.attachIncidentUpdates(list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// CreateIncident stores a new incident and fills in its id.
func (s *Store) CreateIncident(in *model.Incident) error {
	res, err := s.db.Exec(`INSERT INTO incidents
		(kind, title, body, node_ids, starts_at, ends_at, resolved_at, created_at, updated_at, hide_in_sub)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Kind, in.Title, in.Body, incidentNodes(in.NodeIDs), in.StartsAt, in.EndsAt,
		in.ResolvedAt, in.CreatedAt, in.UpdatedAt, boolToInt(in.HideInSub))
	if err != nil {
		return err
	}
	in.ID, err = res.LastInsertId()
	return err
}

// UpdateIncident rewrites what the operator wrote and reports whether the incident
// still existed. Whether it is resolved is not touched: ResolveIncident owns that,
// so an edit racing a resolve cannot undo it.
func (s *Store) UpdateIncident(in *model.Incident) (bool, error) {
	res, err := s.db.Exec(`UPDATE incidents SET kind = ?, title = ?, body = ?, node_ids = ?,
		starts_at = ?, ends_at = ?, updated_at = ?, hide_in_sub = ? WHERE id = ?`,
		in.Kind, in.Title, in.Body, incidentNodes(in.NodeIDs), in.StartsAt, in.EndsAt,
		in.UpdatedAt, boolToInt(in.HideInSub), in.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ResolveIncident marks an incident resolved at `at`, with the closing note as the
// last step of its course when one is given, in one transaction; it reports whether
// this call did it — false when it was already resolved (or is gone).
func (s *Store) ResolveIncident(id, at int64, note string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`UPDATE incidents SET resolved_at = ?, updated_at = ?
		WHERE id = ? AND resolved_at = 0`, at, at, id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if note != "" {
		if _, err := tx.Exec(`INSERT INTO incident_updates (incident_id, stage, body, created_at) VALUES (?, ?, ?, ?)`,
			id, model.IncidentStageResolved, note, at); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// DeleteIncident removes an incident and reports whether there was one.
func (s *Store) DeleteIncident(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM incidents WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AddIncidentUpdate records a step in an incident's course and fills in its id;
// false when the incident is gone. The incident's own updated_at stays: it says when
// the entry itself was last edited, and the course carries its own times.
func (s *Store) AddIncidentUpdate(u *model.IncidentUpdate) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM incidents WHERE id = ?`, u.IncidentID).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return false, nil
	}
	res, err := tx.Exec(`INSERT INTO incident_updates (incident_id, stage, body, created_at) VALUES (?, ?, ?, ?)`,
		u.IncidentID, u.Stage, u.Body, u.CreatedAt)
	if err != nil {
		return false, err
	}
	if u.ID, err = res.LastInsertId(); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// DeleteIncidentUpdate removes one update and reports which incident it belonged
// to (0 when there was none).
func (s *Store) DeleteIncidentUpdate(id int64) (int64, error) {
	var incident int64
	err := s.db.QueryRow(`DELETE FROM incident_updates WHERE id = ? RETURNING incident_id`, id).Scan(&incident)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return incident, err
}

// CompensationCandidate is a user whose subscription was running when an incident
// began: who may be given time back for it.
type CompensationCandidate struct {
	ID       int64
	Name     string
	ExpireAt int64
	TgChatID int64
}

// SnapshotIncidents records, for every incident under way at now that has none yet,
// the users whose term is running: who it hits. Each is taken once.
func (s *Store) SnapshotIncidents(now int64) error {
	rows, err := s.db.Query(`SELECT id FROM incidents
		WHERE snapshot_at = 0 AND resolved_at = 0 AND starts_at <= ?
		  AND (kind <> 'maintenance' OR ends_at = 0 OR ends_at > ?)`, now, now)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		err := s.withTx(func(tx *sql.Tx) error {
			res, err := tx.Exec(`UPDATE incidents SET snapshot_at = ? WHERE id = ? AND snapshot_at = 0`, now, id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			_, err = tx.Exec(`INSERT OR IGNORE INTO incident_users (incident_id, user_id)
				SELECT ?, id FROM users WHERE enabled = 1 AND expire_at > ?`, id, now)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// CompensationCandidates returns who an incident hit, among the enabled users who
// still have a dated term (one with no end, or on hold, lost nothing to wait out):
// those recorded when it began, or — for an entry without that record — those whose
// term runs past its start and who existed before its end.
//
// It walks the whole users table, so it reads through the writer: the read pool is for
// bounded lookups.
func (s *Store) CompensationCandidates(in *model.Incident, to int64) ([]CompensationCandidate, error) {
	query := `SELECT id, name, expire_at, tg_chat_id FROM users
		WHERE enabled = 1 AND expire_at > ? AND created_at < ?`
	args := []any{in.StartsAt, to}
	if in.SnapshotAt != 0 {
		query = `SELECT u.id, u.name, u.expire_at, u.tg_chat_id FROM incident_users i
			JOIN users u ON u.id = i.user_id
			WHERE i.incident_id = ? AND u.enabled = 1 AND u.expire_at > 0`
		args = []any{in.ID}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CompensationCandidate
	for rows.Next() {
		var c CompensationCandidate
		if err := rows.Scan(&c.ID, &c.Name, &c.ExpireAt, &c.TgChatID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ErrCompensated is an incident whose users were already given time back.
var ErrCompensated = errors.New("incident already compensated")

// CompensateIncident marks the incident compensated and moves each user's expiry
// from what it was read as (from) to what it becomes (to), all in one transaction.
// A user whose expiry changed since it was read is left out. It returns the users it
// moved; ErrCompensated when the incident was compensated before (or is gone).
func (s *Store) CompensateIncident(id, at, seconds int64, from, to map[int64]int64) ([]int64, error) {
	var moved []int64
	err := s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE incidents SET compensated_at = ?, compensated_seconds = ?
			WHERE id = ? AND compensated_at = 0`, at, seconds, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrCompensated
		}
		for uid, expire := range to {
			r, err := tx.Exec(`UPDATE users SET expire_at = ? WHERE id = ? AND expire_at = ?`, expire, uid, from[uid])
			if err != nil {
				return err
			}
			if n, _ := r.RowsAffected(); n > 0 {
				moved = append(moved, uid)
			}
		}
		_, err = tx.Exec(`UPDATE incidents SET compensated_users = ? WHERE id = ?`, len(moved), id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return moved, nil
}
