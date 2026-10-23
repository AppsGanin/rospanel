package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/AppsGanin/rospanel/internal/model"
)

// External subscriptions and the servers read from them (model.ExtSubscription,
// model.ExtServer). A server's link carries a foreign credential and is encrypted
// at rest like the panel's own secrets.

// extServerSelect reads a server with the relay its subscription sets (model.ExtServer).
const extServerSelect = `SELECT es.id, es.sub_id, es.key, es.name, es.protocol, es.host, es.port, es.link,
	es.enabled, es.seen_at, sub.routing, es.custom_name
	FROM ext_servers es JOIN ext_subscriptions sub ON sub.id = es.sub_id`

// CreateExtSubscription stores a source and returns its id. Empty identity fields mean
// the panel's own defaults (see extsub.Headers), which is the normal case.
func (s *Store) CreateExtSubscription(name, source string, id model.ExtIdentity) (int64, error) {
	var newID int64
	err := s.db.QueryRow(
		`INSERT INTO ext_subscriptions (name, source, hwid, device_os, os_version, device_model, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		name, encField(source), id.HWID, id.DeviceOS, id.OSVersion, id.DeviceModel, id.UserAgent,
	).Scan(&newID)
	return newID, err
}

// ExtSubscriptions lists every subscription, oldest first — the order the
// operator added them, which is the order they read the list in.
func (s *Store) ExtSubscriptions() ([]model.ExtSubscription, error) {
	relays, err := s.extRelays()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, name, source, hwid, device_os, os_version, device_model, user_agent,
		       enabled, last_fetch_at, last_ok_at, last_error, server_count, created_at,
		       routing
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Relays = relaysOrEmpty(relays[out[i].ID])
	}
	return out, nil
}

// extRelays reads every subscription's relays: sub id → (server, lane), in order.
func (s *Store) extRelays() (map[int64][]model.ExtRelayVia, error) {
	rows, err := s.rdb.Query(`SELECT sub_id, server_id, lane FROM ext_relays ORDER BY sub_id, server_id, lane`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]model.ExtRelayVia{}
	for rows.Next() {
		var sub int64
		var v model.ExtRelayVia
		if err := rows.Scan(&sub, &v.ServerID, &v.Lane); err != nil {
			return nil, err
		}
		out[sub] = append(out[sub], v)
	}
	return out, rows.Err()
}

func relaysOrEmpty(v []model.ExtRelayVia) []model.ExtRelayVia {
	if v == nil {
		return []model.ExtRelayVia{}
	}
	return v
}

// ExtSubscription reads one subscription; nil when there is none with that id.
func (s *Store) ExtSubscription(id int64) (*model.ExtSubscription, error) {
	row := s.db.QueryRow(`SELECT id, name, source, hwid, device_os, os_version, device_model, user_agent,
		       enabled, last_fetch_at, last_ok_at, last_error, server_count, created_at,
		       routing
		FROM ext_subscriptions WHERE id = ?`, id)
	x, err := scanExtSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	relays, err := s.extRelays()
	if err != nil {
		return nil, err
	}
	x.Relays = relaysOrEmpty(relays[x.ID])
	return &x, nil
}

func scanExtSubscription(r rowScanner) (model.ExtSubscription, error) {
	var x model.ExtSubscription
	var enabled int
	var routing string
	if err := r.Scan(&x.ID, &x.Name, &x.Source,
		&x.Identity.HWID, &x.Identity.DeviceOS, &x.Identity.OSVersion,
		&x.Identity.DeviceModel, &x.Identity.UserAgent,
		&enabled, &x.LastFetchAt, &x.LastOKAt, &x.LastError, &x.ServerCount, &x.CreatedAt,
		&routing); err != nil {
		return x, err
	}
	x.Routing = extRoutingOf(routing)
	x.Enabled = enabled != 0
	x.Source = decField(x.Source)
	return x, nil
}

// SetExtSubscriptionSource replaces a subscription's name, where it is read from and the
// device identity it presents; the next sync uses the last two. They are written together
// because the editor shows them together: an operator changing the source usually means
// a different upstream, where the old device id means nothing.
func (s *Store) SetExtSubscriptionSource(id int64, name, source string, ident model.ExtIdentity) error {
	_, err := s.db.Exec(
		`UPDATE ext_subscriptions SET name = ?, source = ?, hwid = ?, device_os = ?, os_version = ?,
		        device_model = ?, user_agent = ? WHERE id = ?`,
		name, encField(source), ident.HWID, ident.DeviceOS, ident.OSVersion,
		ident.DeviceModel, ident.UserAgent, id)
	return err
}

// SetExtSubscriptionRelays replaces the lanes of our servers a subscription is relayed
// through; none hands its servers out as they are.
func (s *Store) SetExtSubscriptionRelays(id int64, relays []model.ExtRelayVia) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM ext_relays WHERE sub_id = ?`, id); err != nil {
			return err
		}
		for _, v := range relays {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO ext_relays (sub_id, server_id, lane) VALUES (?, ?, ?)`,
				id, v.ServerID, v.Lane); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetExtSubscriptionRouting stores how a relayed subscription's traffic leaves the
// relay server; the default one is stored as empty.
func (s *Store) SetExtSubscriptionRouting(id int64, r model.ExtRouting) error {
	raw := ""
	if !r.IsZero() {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	_, err := s.db.Exec(`UPDATE ext_subscriptions SET routing = ? WHERE id = ?`, raw, id)
	return err
}

// extRoutingOf reads a stored routing; empty or unreadable is the default one.
func extRoutingOf(raw string) model.ExtRouting {
	var r model.ExtRouting
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &r)
	}
	if r.Rules == nil {
		r.Rules = []model.ExtRule{}
	}
	return r
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
	if _, err := tx.Exec(`DELETE FROM ext_relays WHERE sub_id = ?`, id); err != nil {
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
	return s.queryExtServers(extServerSelect + ` ORDER BY es.sub_id, es.id`)
}

// EnabledExtServers is what a subscription may hand a user: servers switched on,
// from sources switched on.
func (s *Store) EnabledExtServers() ([]model.ExtServer, error) {
	return s.queryExtServers(extServerSelect + ` WHERE es.enabled = 1 AND sub.enabled = 1 ORDER BY es.sub_id, es.id`)
}

// SetExtServerName sets the operator's name for a server ("" puts the source's back)
// and reports whether there was such a server.
func (s *Store) SetExtServerName(id int64, name string) (bool, error) {
	res, err := s.db.Exec(`UPDATE ext_servers SET custom_name = ? WHERE id = ?`, name, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
	// The relays first: a second query while rows hold a connection of the bounded
	// read pool waits for one, and enough readers doing it at once wait forever.
	relays, err := s.extRelays()
	if err != nil {
		return nil, err
	}
	rows, err := s.rdb.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ExtServer{}
	for rows.Next() {
		var e model.ExtServer
		var enabled int
		var routing string
		if err := rows.Scan(&e.ID, &e.SubID, &e.Key, &e.Name, &e.Protocol, &e.Host, &e.Port, &e.Link, &enabled, &e.SeenAt,
			&routing, &e.CustomName); err != nil {
			return nil, fmt.Errorf("ext_servers: %w", err)
		}
		e.SourceName = e.Name
		if e.CustomName != "" {
			e.Name = e.CustomName
		}
		e.Enabled = enabled != 0
		e.Routing = extRoutingOf(routing)
		e.Relays = relays[e.SubID]
		e.Link = decField(e.Link)
		out = append(out, e)
	}
	return out, rows.Err()
}
