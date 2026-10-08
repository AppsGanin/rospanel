package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Plugins written in the panel (see migration 0117). Saved on each edit an admin
// makes: plain single writes.

const draftMeta = `id, name, mode, plugin_id, version, created_by, created_at, updated_at`

// ListPluginDrafts returns every draft, the last edited first, without its files.
func (s *Store) ListPluginDrafts() ([]model.PluginDraft, error) {
	rows, err := s.db.Query(`SELECT ` + draftMeta + ` FROM plugin_drafts ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.PluginDraft{}
	for rows.Next() {
		var d model.PluginDraft
		if err := rows.Scan(&d.ID, &d.Name, &d.Mode, &d.PluginID, &d.Version, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetPluginDraft returns one draft with its files, or sql.ErrNoRows.
func (s *Store) GetPluginDraft(id int64) (*model.PluginDraft, error) {
	var d model.PluginDraft
	var files string
	err := s.db.QueryRow(`SELECT `+draftMeta+`, files FROM plugin_drafts WHERE id = ?`, id).
		Scan(&d.ID, &d.Name, &d.Mode, &d.PluginID, &d.Version, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &files)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(decField(files)), &d.Files); err != nil {
		return nil, errors.New("plugin draft: its files cannot be read (wrong secrets.key?)")
	}
	return &d, nil
}

// SavePluginDraft inserts a draft (ID 0, the new id is set) or replaces one.
func (s *Store) SavePluginDraft(d *model.PluginDraft) error {
	raw, err := json.Marshal(d.Files)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	d.UpdatedAt = now
	if d.ID == 0 {
		d.CreatedAt = now
		res, err := s.db.Exec(`INSERT INTO plugin_drafts (name, mode, plugin_id, version, files, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, d.Name, d.Mode, d.PluginID, d.Version, encField(string(raw)), d.CreatedBy, now, now)
		if err != nil {
			return err
		}
		d.ID, err = res.LastInsertId()
		return err
	}
	res, err := s.db.Exec(`UPDATE plugin_drafts SET name = ?, mode = ?, plugin_id = ?, version = ?, files = ?, updated_at = ? WHERE id = ?`,
		d.Name, d.Mode, d.PluginID, d.Version, encField(string(raw)), now, d.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeletePluginDraft removes a draft.
func (s *Store) DeletePluginDraft(id int64) error {
	_, err := s.db.Exec(`DELETE FROM plugin_drafts WHERE id = ?`, id)
	return err
}
