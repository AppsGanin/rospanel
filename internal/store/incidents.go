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

const incidentCols = `id, kind, title, body, node_ids, starts_at, ends_at, resolved_at, created_at, updated_at, hide_in_sub`

func scanIncident(sc interface{ Scan(...any) error }) (model.Incident, error) {
	var (
		in    model.Incident
		nodes string
	)
	var hide int
	if err := sc.Scan(&in.ID, &in.Kind, &in.Title, &in.Body, &nodes, &in.StartsAt,
		&in.EndsAt, &in.ResolvedAt, &in.CreatedAt, &in.UpdatedAt, &hide); err != nil {
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
