package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/AppsGanin/rospanel/internal/model"
)

// External subscriptions and the servers read from them (model.ExtSubscription,
// model.ExtServer). A server's link carries a foreign credential and is encrypted
// at rest like the panel's own secrets.

const extServerCols = `id, sub_id, key, name, protocol, host, port, link, enabled, seen_at`

// CreateExtSubscription stores a source and returns its id.
func (s *Store) CreateExtSubscription(name, source string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`INSERT INTO ext_subscriptions (name, source) VALUES (?, ?) RETURNING id`,
		name, encField(source)).Scan(&id)
	return id, err
}

// ExtSubscriptions lists every subscription, oldest first — the order the
// operator added them, which is the order they read the list in.
func (s *Store) ExtSubscriptions() ([]model.ExtSubscription, error) {
	rows, err := s.db.Query(`SELECT id, name, source, enabled, last_fetch_at, last_ok_at, last_error, server_count, created_at
		FROM ext_subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ExtSubscription{}
	for rows.Next() {
		x, err := scanExtSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ExtSubscription reads one subscription; nil when there is none with that id.
func (s *Store) ExtSubscription(id int64) (*model.ExtSubscription, error) {
	row := s.db.QueryRow(`SELECT id, name, source, enabled, last_fetch_at, last_ok_at, last_error, server_count, created_at
		FROM ext_subscriptions WHERE id = ?`, id)
	x, err := scanExtSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}

func scanExtSubscription(r rowScanner) (model.ExtSubscription, error) {
	var x model.ExtSubscription
	var enabled int
	if err := r.Scan(&x.ID, &x.Name, &x.Source, &enabled, &x.LastFetchAt, &x.LastOKAt, &x.LastError, &x.ServerCount, &x.CreatedAt); err != nil {
		return x, err
	}
	x.Enabled = enabled != 0
	x.Source = decField(x.Source)
	return x, nil
}

// SetExtSubscriptionSource replaces where a subscription is read from; the next
// sync reads the new source.
func (s *Store) SetExtSubscriptionSource(id int64, source string) error {
	_, err := s.db.Exec(`UPDATE ext_subscriptions SET source = ? WHERE id = ?`, encField(source), id)
	return err
}

// SetExtSubscriptionEnabled switches a whole source on or off; its servers keep
// their own flags for when it comes back.
func (s *Store) SetExtSubscriptionEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE ext_subscriptions SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	return err
}

// MarkExtSubscriptionSync records the outcome of a read: when, how many servers,
// and the error if it failed (the previous servers are kept on a failure — a
// source that is down for an hour must not empty the list).
func (s *Store) MarkExtSubscriptionSync(id int64, count int, errText string, now int64) error {
	if errText != "" {
		_, err := s.db.Exec(`UPDATE ext_subscriptions SET last_fetch_at = ?, last_error = ? WHERE id = ?`, now, errText, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE ext_subscriptions SET last_fetch_at = ?, last_ok_at = ?, last_error = '', server_count = ? WHERE id = ?`,
		now, now, count, id)
	return err
}

// DeleteExtSubscription removes a source, its servers, and every group grant that
// pointed at one of them — one transaction, so no group keeps a token for a server
// that no longer exists.
func (s *Store) DeleteExtSubscription(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM group_grants WHERE token IN (SELECT 'ext:' || id FROM ext_servers WHERE sub_id = ?)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM ext_servers WHERE sub_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM ext_subscriptions WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceExtServers reconciles a subscription's servers with what a read found:
// a known key is updated in place (its on/off flag untouched), a new key is added
// switched on, a key no longer listed is dropped together with its grants. Returns
// how many of each.
func (s *Store) ReplaceExtServers(subID int64, found []model.ExtServer, now int64) (added, updated, removed int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id, key FROM ext_servers WHERE sub_id = ?`, subID)
	if err != nil {
		return 0, 0, 0, err
	}
	existing := map[string]int64{}
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return 0, 0, 0, err
		}
		existing[key] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, 0, err
	}

	seen := map[string]bool{}
	for _, e := range found {
		if seen[e.Key] {
			continue
		}
		seen[e.Key] = true
		if id, ok := existing[e.Key]; ok {
			if _, err := tx.Exec(`UPDATE ext_servers SET name = ?, protocol = ?, host = ?, port = ?, link = ?, seen_at = ? WHERE id = ?`,
				e.Name, e.Protocol, e.Host, e.Port, encField(e.Link), now, id); err != nil {
				return 0, 0, 0, err
			}
			updated++
			continue
		}
		if _, err := tx.Exec(`INSERT INTO ext_servers (sub_id, key, name, protocol, host, port, link, enabled, seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`,
			subID, e.Key, e.Name, e.Protocol, e.Host, e.Port, encField(e.Link), now); err != nil {
			return 0, 0, 0, err
		}
		added++
	}
	for key, id := range existing {
		if seen[key] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM group_grants WHERE token = ?`, model.ExtToken(id)); err != nil {
			return 0, 0, 0, err
		}
		if _, err := tx.Exec(`DELETE FROM ext_servers WHERE id = ?`, id); err != nil {
			return 0, 0, 0, err
		}
		removed++
	}
	return added, updated, removed, tx.Commit()
}

// ExtServers lists every server of every subscription, for the management UI
// and the group editor.
func (s *Store) ExtServers() ([]model.ExtServer, error) {
	return s.queryExtServers(`SELECT ` + extServerCols + ` FROM ext_servers ORDER BY sub_id, id`)
}

// EnabledExtServers is what a subscription may hand a user: servers switched on,
// from sources switched on.
func (s *Store) EnabledExtServers() ([]model.ExtServer, error) {
	return s.queryExtServers(`SELECT ` + extServerCols + ` FROM ext_servers
		WHERE enabled = 1 AND sub_id IN (SELECT id FROM ext_subscriptions WHERE enabled = 1)
		ORDER BY sub_id, id`)
}

// SetExtServerEnabled switches one server on or off.
func (s *Store) SetExtServerEnabled(id int64, enabled bool) (bool, error) {
	res, err := s.db.Exec(`UPDATE ext_servers SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetExtSubscriptionServersEnabled switches every server of a source at once.
func (s *Store) SetExtSubscriptionServersEnabled(subID int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE ext_servers SET enabled = ? WHERE sub_id = ?`, boolToInt(enabled), subID)
	return err
}

func (s *Store) queryExtServers(query string, args ...any) ([]model.ExtServer, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ExtServer{}
	for rows.Next() {
		var e model.ExtServer
		var enabled int
		if err := rows.Scan(&e.ID, &e.SubID, &e.Key, &e.Name, &e.Protocol, &e.Host, &e.Port, &e.Link, &enabled, &e.SeenAt); err != nil {
			return nil, fmt.Errorf("ext_servers: %w", err)
		}
		e.Enabled = enabled != 0
		e.Link = decField(e.Link)
		out = append(out, e)
	}
	return out, rows.Err()
}
