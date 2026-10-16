package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Status page incidents and maintenance windows (migration 0127).

const incidentCols = `id, kind, title, body, node_ids, starts_at, ends_at, resolved_at, created_at, updated_at`

func scanIncident(sc interface{ Scan(...any) error }) (model.Incident, error) {
	var (
		in    model.Incident
		nodes string
	)
	if err := sc.Scan(&in.ID, &in.Kind, &in.Title, &in.Body, &nodes, &in.StartsAt,
		&in.EndsAt, &in.ResolvedAt, &in.CreatedAt, &in.UpdatedAt); err != nil {
		return in, err
	}
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

// ListIncidents returns up to limit incidents, the latest to start first.
func (s *Store) ListIncidents(limit int) ([]model.Incident, error) {
	rows, err := s.db.Query(`SELECT `+incidentCols+` FROM incidents
		ORDER BY starts_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Incident{}
	for rows.Next() {
		in, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// IncidentsForPage returns what the status page can show at now: everything not
// over yet, and what ended since `since`.
func (s *Store) IncidentsForPage(now, since int64) ([]model.Incident, error) {
	rows, err := s.db.Query(`SELECT `+incidentCols+` FROM incidents
		WHERE (resolved_at = 0 AND (kind <> 'maintenance' OR ends_at = 0 OR ends_at > ?))
		   OR resolved_at >= ? OR (resolved_at = 0 AND ends_at >= ?)
		ORDER BY starts_at DESC, id DESC`, now, since, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Incident{}
	for rows.Next() {
		in, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
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
	return &in, nil
}

// CreateIncident stores a new incident and fills in its id.
func (s *Store) CreateIncident(in *model.Incident) error {
	res, err := s.db.Exec(`INSERT INTO incidents
		(kind, title, body, node_ids, starts_at, ends_at, resolved_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Kind, in.Title, in.Body, incidentNodes(in.NodeIDs), in.StartsAt, in.EndsAt,
		in.ResolvedAt, in.CreatedAt, in.UpdatedAt)
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
		starts_at = ?, ends_at = ?, updated_at = ? WHERE id = ?`,
		in.Kind, in.Title, in.Body, incidentNodes(in.NodeIDs), in.StartsAt, in.EndsAt,
		in.UpdatedAt, in.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ResolveIncident marks an incident resolved at `at` and reports whether this call
// did it — false when it was already resolved (or is gone).
func (s *Store) ResolveIncident(id, at int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE incidents SET resolved_at = ?, updated_at = ?
		WHERE id = ? AND resolved_at = 0`, at, at, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
