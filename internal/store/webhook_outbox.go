package store

import (
	"cmp"
	"database/sql"
	"errors"
	"slices"
	"time"
)

// WebhookDelivery is one pending delivery of one event to one subscriber: an
// endpoint (HookID) or a plugin (PluginID, HookID 0).
type WebhookDelivery struct {
	ID       int64
	HookID   int64
	PluginID string
	Event    string
	Body     []byte
	Attempt  int // attempts made so far
	dueAt    int64
}

// EnqueueWebhookDeliveries stores deliveries due now, in one transaction — a bulk
// action's thousand rows are one write.
func (s *Store) EnqueueWebhookDeliveries(ds []WebhookDelivery) error {
	if len(ds) == 0 {
		return nil
	}
	now := time.Now().Unix()
	return s.withTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT INTO webhook_outbox (hook_id, plugin_id, event, body, attempt, next_at, created_at)
			VALUES (?, ?, ?, ?, 0, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, d := range ds {
			if _, err := stmt.Exec(d.HookID, d.PluginID, d.Event, d.Body, now, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// LeaseWebhookDeliveries takes up to limit endpoint deliveries due at now and moves
// them out of reach until now+lease, so a delivery being sent is not taken again —
// and one whose sender died with the panel comes back once the lease runs out.
func (s *Store) LeaseWebhookDeliveries(now, lease int64, limit int) ([]WebhookDelivery, error) {
	// The dispatcher asks every few seconds and nearly always finds nothing: answer
	// that from the read pool, leaving the single writer alone.
	var due int
	if err := s.rdb.QueryRow(`SELECT EXISTS(SELECT 1 FROM webhook_outbox WHERE next_at <= ? AND plugin_id = '')`, now).Scan(&due); err == nil && due == 0 {
		return nil, nil
	}
	var out []WebhookDelivery
	err := s.withTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id, hook_id, plugin_id, event, body, attempt FROM webhook_outbox
			WHERE next_at <= ? AND plugin_id = '' ORDER BY next_at, id LIMIT ?`, now, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d WebhookDelivery
			if err := rows.Scan(&d.ID, &d.HookID, &d.PluginID, &d.Event, &d.Body, &d.Attempt); err != nil {
				rows.Close()
				return err
			}
			out = append(out, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, d := range out {
			if _, err := tx.Exec(`UPDATE webhook_outbox SET next_at = ? WHERE id = ?`, now+lease, d.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// LeasePluginDeliveries is LeaseWebhookDeliveries for the plugins' rows. The two are
// leased apart so a slow plugin never holds an endpoint's delivery back.
//
// At most one row per plugin — its oldest due one — and only for the plugins
// given (those taking events, without a delivery in flight): calls into a plugin
// run one at a time, so a second row would only wait. Each plugin's row is found by
// its own index on the read pool; the writer only claims what was found.
func (s *Store) LeasePluginDeliveries(now, lease int64, limit int, plugins []string) ([]WebhookDelivery, error) {
	var found []WebhookDelivery
	for _, p := range plugins {
		var d WebhookDelivery
		err := s.rdb.QueryRow(`SELECT id, hook_id, plugin_id, event, body, attempt, next_at FROM webhook_outbox
			WHERE plugin_id = ? AND plugin_id <> '' AND next_at <= ? ORDER BY next_at, id LIMIT 1`, p, now). // <> '' lets SQLite use the partial index
			Scan(&d.ID, &d.HookID, &d.PluginID, &d.Event, &d.Body, &d.Attempt, &d.dueAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		found = append(found, d)
	}
	if len(found) == 0 {
		return nil, nil
	}
	slices.SortFunc(found, func(a, b WebhookDelivery) int {
		if a.dueAt != b.dueAt {
			return cmp.Compare(a.dueAt, b.dueAt)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if len(found) > limit {
		found = found[:limit]
	}
	var out []WebhookDelivery
	err := s.withTx(func(tx *sql.Tx) error {
		for _, d := range found {
			// Claimed only if still due: another lease may have taken it meanwhile.
			res, err := tx.Exec(`UPDATE webhook_outbox SET next_at = ? WHERE id = ? AND next_at <= ?`, now+lease, d.ID, now)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				out = append(out, d)
			}
		}
		return nil
	})
	return out, err
}

// FinishWebhookDelivery removes a delivery that went out or ran out of attempts.
func (s *Store) FinishWebhookDelivery(id int64) error {
	_, err := s.db.Exec(`DELETE FROM webhook_outbox WHERE id = ?`, id)
	return err
}

// RetryWebhookDelivery records a failed attempt and when to try again.
func (s *Store) RetryWebhookDelivery(id int64, attempt int, nextAt int64) error {
	_, err := s.db.Exec(`UPDATE webhook_outbox SET attempt = ?, next_at = ? WHERE id = ?`, attempt, nextAt, id)
	return err
}

// PendingWebhookDeliveries counts the deliveries not yet out.
func (s *Store) PendingWebhookDeliveries() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM webhook_outbox`).Scan(&n)
	return n, err
}

// DropPluginDeliveries removes every pending delivery to a plugin — one that was
// switched off, paused or removed gets nothing it missed meanwhile.
func (s *Store) DropPluginDeliveries(pluginID string) error {
	_, err := s.db.Exec(`DELETE FROM webhook_outbox WHERE plugin_id = ?`, pluginID)
	return err
}
