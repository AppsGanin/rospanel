package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Installed plugins (see internal/plugin). Rows change on install, update, toggle
// and settings — operator actions, so these are plain single writes.

const pluginCols = `id, version, manifest, package, sha256, enabled, status, status_error,
	granted_perms, granted_net, config, prev_package, prev_version, sort, installed_at, updated_at`

// ListPlugins returns every installed plugin in their configured order.
func (s *Store) ListPlugins() ([]model.Plugin, error) {
	rows, err := s.db.Query(`SELECT ` + pluginCols + ` FROM plugins ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Plugin
	for rows.Next() {
		p, err := scanPlugin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlugin returns one plugin, or nil when it is not installed.
func (s *Store) GetPlugin(id string) (*model.Plugin, error) {
	p, err := scanPlugin(s.db.QueryRow(`SELECT `+pluginCols+` FROM plugins WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// SavePlugin inserts or replaces a plugin's row. installed_at survives a replace.
func (s *Store) SavePlugin(p model.Plugin) error {
	cfg, err := json.Marshal(p.Config)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	if p.InstalledAt == 0 {
		p.InstalledAt = now
	}
	_, err = s.db.Exec(`INSERT INTO plugins (`+pluginCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET version = excluded.version, manifest = excluded.manifest,
			package = excluded.package, sha256 = excluded.sha256, enabled = excluded.enabled,
			status = excluded.status, status_error = excluded.status_error,
			granted_perms = excluded.granted_perms, granted_net = excluded.granted_net,
			config = excluded.config, prev_package = excluded.prev_package,
			prev_version = excluded.prev_version, sort = excluded.sort, updated_at = excluded.updated_at`,
		p.ID, p.Version, p.Manifest, p.Package, p.SHA256, boolToInt(p.Enabled), p.Status, p.StatusError,
		strings.Join(p.GrantedPerms, ","), strings.Join(p.GrantedNet, ","), encField(string(cfg)),
		p.PrevPackage, p.PrevVersion, p.Sort, p.InstalledAt, now)
	return err
}

// SetPluginState records whether a plugin is on and how it is doing.
func (s *Store) SetPluginState(id string, enabled bool, status, statusErr string) error {
	_, err := s.db.Exec(`UPDATE plugins SET enabled = ?, status = ?, status_error = ?, updated_at = ? WHERE id = ?`,
		boolToInt(enabled), status, statusErr, time.Now().Unix(), id)
	return err
}

// SetPluginConfig replaces a plugin's settings (stored encrypted).
func (s *Store) SetPluginConfig(id string, cfg map[string]string) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE plugins SET config = ?, updated_at = ? WHERE id = ?`,
		encField(string(raw)), time.Now().Unix(), id)
	return err
}

// DeletePlugin removes a plugin's row.
func (s *Store) DeletePlugin(id string) error {
	_, err := s.db.Exec(`DELETE FROM plugins WHERE id = ?`, id)
	return err
}

func scanPlugin(r rowScanner) (model.Plugin, error) {
	var p model.Plugin
	var enabled int
	var perms, nets, cfg string
	if err := r.Scan(&p.ID, &p.Version, &p.Manifest, &p.Package, &p.SHA256, &enabled, &p.Status, &p.StatusError,
		&perms, &nets, &cfg, &p.PrevPackage, &p.PrevVersion, &p.Sort, &p.InstalledAt, &p.UpdatedAt); err != nil {
		return p, err
	}
	p.Enabled = enabled != 0
	p.GrantedPerms = splitList(perms)
	p.GrantedNet = splitList(nets)
	p.Config = decodeProviderConfig(cfg)
	return p, nil
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// PluginCatalogURL is the catalog index the panel reads; "" for the official one.
func (s *Store) PluginCatalogURL() (string, error) {
	var u string
	err := s.db.QueryRow(`SELECT url FROM plugin_catalog WHERE id = 1`).Scan(&u)
	return u, err
}

// SetPluginCatalogURL sets the catalog index ("" = the official one).
func (s *Store) SetPluginCatalogURL(u string) error {
	_, err := s.db.Exec(`UPDATE plugin_catalog SET url = ? WHERE id = 1`, u)
	return err
}

// PluginUpdatesNotified is {plugin: version} of the new versions already announced.
func (s *Store) PluginUpdatesNotified() (map[string]string, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT notified FROM plugin_catalog WHERE id = 1`).Scan(&raw); err != nil {
		return nil, err
	}
	out := map[string]string{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out, nil
}

// SetPluginUpdatesNotified records the new versions announced.
func (s *Store) SetPluginUpdatesNotified(m map[string]string) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE plugin_catalog SET notified = ? WHERE id = 1`, string(b))
	return err
}
