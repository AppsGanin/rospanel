package store

import (
	"database/sql"
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
	return s.leaseDeliveries(now, lease, limit, false)
}

// LeasePluginDeliveries is LeaseWebhookDeliveries for the plugins' rows. The two are
// leased apart so a slow plugin never holds an endpoint's delivery back.
func (s *Store) LeasePluginDeliveries(now, lease int64, limit int) ([]WebhookDelivery, error) {
	return s.leaseDeliveries(now, lease, limit, true)
}

func (s *Store) leaseDeliveries(now, lease int64, limit int, plugins bool) ([]WebhookDelivery, error) {
	which := `plugin_id = ''`
	if plugins {
		which = `plugin_id <> ''`
	}
	// The dispatcher asks every few seconds and nearly always finds nothing: answer
	// that from the read pool, leaving the single writer alone.
	var due int
	if err := s.rdb.QueryRow(`SELECT EXISTS(SELECT 1 FROM webhook_outbox WHERE next_at <= ? AND `+which+`)`, now).Scan(&due); err == nil && due == 0 {
		return nil, nil
	}
	var out []WebhookDelivery
	err := s.withTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id, hook_id, plugin_id, event, body, attempt FROM webhook_outbox
			WHERE next_at <= ? AND `+which+` ORDER BY next_at, id LIMIT ?`, now, limit)
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
