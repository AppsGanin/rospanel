package store

import (
	"database/sql"

	"github.com/AppsGanin/rospanel/internal/model"
)

// maxConfigSnapshots caps how many routing snapshots are kept. Enough to undo a run of
// bad edits, bounded so an auto-snapshot on every routing change can't grow forever.
const maxConfigSnapshots = 30

// CreateConfigSnapshot stores one routing snapshot and trims the history to the cap.
// auto marks the ones taken automatically before a change (vs. an operator's manual
// save-point).
func (s *Store) CreateConfigSnapshot(label string, auto bool, routingJSON string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO config_snapshots (created_at, label, auto, routing_json)
			 VALUES (unixepoch(), ?, ?, ?)`,
			label, boolToInt(auto), routingJSON,
		); err != nil {
			return err
		}
		// Trim: keep the newest maxConfigSnapshots rows.
		_, err := tx.Exec(
			`DELETE FROM config_snapshots WHERE id NOT IN (
			   SELECT id FROM config_snapshots ORDER BY created_at DESC, id DESC LIMIT ?
			 )`, maxConfigSnapshots)
		return err
	})
}

// ListConfigSnapshots returns the snapshots newest first, without the payload (the
// list view only needs the metadata; the routing JSON is fetched on rollback).
func (s *Store) ListConfigSnapshots() ([]model.ConfigSnapshot, error) {
	rows, err := s.db.Query(
		`SELECT id, created_at, label, auto FROM config_snapshots
		 ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ConfigSnapshot
	for rows.Next() {
		var sn model.ConfigSnapshot
		var auto int
		if err := rows.Scan(&sn.ID, &sn.CreatedAt, &sn.Label, &auto); err != nil {
			return nil, err
		}
		sn.Auto = auto != 0
		out = append(out, sn)
	}
	return out, rows.Err()
}

// ConfigSnapshotRouting returns one snapshot's stored routing JSON, or "" if the id is
// unknown.
func (s *Store) ConfigSnapshotRouting(id int64) (string, error) {
	var routingJSON string
	err := s.db.QueryRow(
		`SELECT routing_json FROM config_snapshots WHERE id = ?`, id).Scan(&routingJSON)
	if err != nil {
		return "", err
	}
	return routingJSON, nil
}

// DeleteConfigSnapshot removes one snapshot.
func (s *Store) DeleteConfigSnapshot(id int64) error {
	_, err := s.db.Exec(`DELETE FROM config_snapshots WHERE id = ?`, id)
	return err
}
