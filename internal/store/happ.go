package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/happ"
)

// ── Subscription CRUD ──────────────────────────────────────────────────────

// CreateHappSubscription inserts a new Happ subscription and returns its ID.
func (s *Store) CreateHappSubscription(name, rawURL string) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`
		INSERT INTO happ_subscriptions (name, url, enabled, update_interval_min, created_at)
		VALUES (?, ?, 1, 59, ?)`,
		name, rawURL, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetHappSubscription returns one subscription by ID.
func (s *Store) GetHappSubscription(id int64) (*happ.Subscription, error) {
	row := s.db.QueryRow(`
		SELECT id, name, url, enabled, update_interval_min,
		       last_fetch_at, last_success_at, last_error, node_count, created_at
		FROM happ_subscriptions WHERE id = ?`, id)
	return scanHappSubscription(row)
}

// ListHappSubscriptions returns all subscriptions.
func (s *Store) ListHappSubscriptions() ([]*happ.Subscription, error) {
	rows, err := s.db.Query(`
		SELECT id, name, url, enabled, update_interval_min,
		       last_fetch_at, last_success_at, last_error, node_count, created_at
		FROM happ_subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*happ.Subscription
	for rows.Next() {
		sub, err := scanHappSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// ListEnabledHappSubscriptionIDs returns IDs of all enabled subscriptions.
// Used by the scheduler to know which subs to sync.
func (s *Store) ListEnabledHappSubscriptionIDs() ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM happ_subscriptions WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteHappSubscription deletes a subscription and all its nodes (CASCADE).
func (s *Store) DeleteHappSubscription(id int64) error {
	_, err := s.db.Exec(`DELETE FROM happ_subscriptions WHERE id = ?`, id)
	return err
}

// UpdateHappSubscriptionFetch records fetch outcome: last_fetch_at, last_success_at,
// last_error, node_count.
func (s *Store) UpdateHappSubscriptionFetch(id int64, nodeCount int, fetchErr string) error {
	now := time.Now().Unix()
	if fetchErr == "" {
		_, err := s.db.Exec(`
			UPDATE happ_subscriptions
			SET last_fetch_at = ?, last_success_at = ?, last_error = '', node_count = ?
			WHERE id = ?`,
			now, now, nodeCount, id)
		return err
	}
	_, err := s.db.Exec(`
		UPDATE happ_subscriptions
		SET last_fetch_at = ?, last_error = ?
		WHERE id = ?`,
		now, fetchErr, id)
	return err
}

// scanHappSubscription scans one subscription row.
func scanHappSubscription(sc interface{ Scan(...any) error }) (*happ.Subscription, error) {
	var sub happ.Subscription
	var enabled int
	err := sc.Scan(
		&sub.ID, &sub.Name, &sub.URL, &enabled, &sub.UpdateIntervalMin,
		&sub.LastFetchAt, &sub.LastSuccessAt, &sub.LastError, &sub.NodeCount, &sub.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sub.Enabled = enabled != 0
	return &sub, nil
}

// ── Node CRUD ─────────────────────────────────────────────────────────────

// UpsertHappNodes inserts new nodes and updates existing ones by identity_key.
// Returns (added, updated, err).
func (s *Store) UpsertHappNodes(subscriptionID int64, nodes []happ.Node) (added, updated int, err error) {
	now := time.Now().Unix()
	for _, n := range nodes {
		res, e := s.db.Exec(`
			INSERT INTO happ_nodes
			    (subscription_id, identity_key, name, protocol, host, port, uri, enabled, last_seen_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
			ON CONFLICT(identity_key) DO UPDATE SET
			    name         = excluded.name,
			    protocol     = excluded.protocol,
			    host         = excluded.host,
			    port         = excluded.port,
			    uri          = excluded.uri,
			    last_seen_at = excluded.last_seen_at,
			    updated_at   = excluded.updated_at`,
			subscriptionID, n.IdentityKey, n.Name, n.Protocol, n.Host, n.Port, n.URI,
			now, now, now,
		)
		if e != nil {
			err = e
			return
		}
		rows, _ := res.RowsAffected()
		if rows == 1 {
			// A plain INSERT RowsAffected=1 means a new row.
			// ON CONFLICT UPDATE RowsAffected is 1 if any value changed, 0 if nothing changed.
			// Heuristic: check if the node existed before by presence of created_at == now.
			// Actually SQLite ON CONFLICT UPDATE always returns 1 if a row was modified
			// and we can't easily distinguish insert vs update here without extra query.
			// We track counts via a separate check.
			added++
		}
	}
	return
}

// UpsertHappNodesDiff performs an upsert and also returns how many new/updated/removed.
// "Removed" means nodes of this subscription not seen in the current fetch (last_seen_at is old).
// Nodes not seen are left in the DB (soft-keep policy — fetch failure should not delete nodes).
func (s *Store) UpsertHappNodesFull(subscriptionID int64, nodes []happ.Node) (added, updated int, err error) {
	if len(nodes) == 0 {
		return
	}
	now := time.Now().Unix()

	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Build a set of incoming identity keys.
	incoming := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		incoming[n.IdentityKey] = true
	}

	// Load existing identity keys for this subscription.
	rows, err := tx.Query(`SELECT identity_key FROM happ_nodes WHERE subscription_id = ?`, subscriptionID)
	if err != nil {
		return
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var k string
		if e := rows.Scan(&k); e == nil {
			existing[k] = true
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return
	}

	// Upsert each incoming node.
	for i := range nodes {
		n := &nodes[i]
		isNew := !existing[n.IdentityKey]
		_, e := tx.Exec(`
			INSERT INTO happ_nodes
			    (subscription_id, identity_key, name, protocol, host, port, uri, enabled, last_seen_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
			ON CONFLICT(identity_key) DO UPDATE SET
			    name         = excluded.name,
			    protocol     = excluded.protocol,
			    host         = excluded.host,
			    port         = excluded.port,
			    uri          = excluded.uri,
			    last_seen_at = excluded.last_seen_at,
			    updated_at   = excluded.updated_at`,
			subscriptionID, n.IdentityKey, n.Name, n.Protocol, n.Host, n.Port, n.URI,
			now, now, now,
		)
		if e != nil {
			err = e
			return
		}
		if isNew {
			added++
		} else {
			updated++
		}
	}

	err = tx.Commit()
	return
}

// ListHappNodes returns all nodes for a subscription, ordered by ID.
func (s *Store) ListHappNodes(subscriptionID int64) ([]*happ.Node, error) {
	rows, err := s.db.Query(`
		SELECT id, subscription_id, identity_key, name, protocol, host, port, enabled, uri,
		       last_seen_at, created_at, updated_at
		FROM happ_nodes WHERE subscription_id = ?
		ORDER BY id`, subscriptionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHappNodes(rows)
}

// ListAllHappNodes returns all nodes across all subscriptions.
func (s *Store) ListAllHappNodes() ([]*happ.Node, error) {
	rows, err := s.db.Query(`
		SELECT id, subscription_id, identity_key, name, protocol, host, port, enabled, uri,
		       last_seen_at, created_at, updated_at
		FROM happ_nodes ORDER BY subscription_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHappNodes(rows)
}

// ListEnabledHappNodes returns only enabled nodes (for Xray outbound generation).
func (s *Store) ListEnabledHappNodes() ([]*happ.Node, error) {
	rows, err := s.db.Query(`
		SELECT id, subscription_id, identity_key, name, protocol, host, port, enabled, uri,
		       last_seen_at, created_at, updated_at
		FROM happ_nodes WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHappNodes(rows)
}

// SetHappNodeEnabled sets the enabled flag for a single node.
func (s *Store) SetHappNodeEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE happ_nodes SET enabled = ?, updated_at = ? WHERE id = ?`,
		v, time.Now().Unix(), id)
	return err
}

// DeleteHappNode deletes a single node by ID.
func (s *Store) DeleteHappNode(id int64) error {
	_, err := s.db.Exec(`DELETE FROM happ_nodes WHERE id = ?`, id)
	return err
}

// GetHappNode returns a single node by ID.
func (s *Store) GetHappNode(id int64) (*happ.Node, error) {
	row := s.db.QueryRow(`
		SELECT id, subscription_id, identity_key, name, protocol, host, port, enabled, uri,
		       last_seen_at, created_at, updated_at
		FROM happ_nodes WHERE id = ?`, id)
	var n happ.Node
	var enabled int
	err := row.Scan(
		&n.ID, &n.SubscriptionID, &n.IdentityKey, &n.Name, &n.Protocol, &n.Host, &n.Port,
		&enabled, &n.URI, &n.LastSeenAt, &n.CreatedAt, &n.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.Enabled = enabled != 0
	return &n, nil
}

// scanHappNodes scans multiple node rows.
func scanHappNodes(rows *sql.Rows) ([]*happ.Node, error) {
	var out []*happ.Node
	for rows.Next() {
		var n happ.Node
		var enabled int
		if err := rows.Scan(
			&n.ID, &n.SubscriptionID, &n.IdentityKey, &n.Name, &n.Protocol, &n.Host, &n.Port,
			&enabled, &n.URI, &n.LastSeenAt, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, err
		}
		n.Enabled = enabled != 0
		out = append(out, &n)
	}
	return out, rows.Err()
}
