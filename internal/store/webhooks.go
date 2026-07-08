package store

import (
	"database/sql"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
)

// joinEvents / splitEvents convert between the []string API shape and the CSV
// stored in the events column.
func joinEvents(ev []string) string { return strings.Join(ev, ",") }

func splitEvents(csv string) []string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	return strings.Split(csv, ",")
}

// CreateWebhook stores a new endpoint, generating a random HMAC signing secret
// (encrypted at rest) and returning the record with the plaintext secret.
func (s *Store) CreateWebhook(url string, events []string) (*model.Webhook, error) {
	secret, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO webhooks (url, secret, events, enabled, created_at) VALUES (?, ?, ?, 1, ?)`,
		url, encField(secret), joinEvents(events), now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.Webhook{
		ID: id, URL: url, Secret: secret, Events: events,
		Enabled: true, CreatedAt: now,
	}, nil
}

// scanWebhooks runs a query and decodes rows (decrypting the secret).
func (s *Store) scanWebhooks(query string, args ...any) ([]model.Webhook, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Webhook
	for rows.Next() {
		var h model.Webhook
		var events, secret string
		var enabled int
		if err := rows.Scan(&h.ID, &h.URL, &secret, &events, &enabled,
			&h.CreatedAt, &h.LastStatus, &h.LastAttemptAt, &h.LastError); err != nil {
			return nil, err
		}
		h.Secret = decField(secret)
		h.Events = splitEvents(events)
		h.Enabled = enabled != 0
		out = append(out, h)
	}
	return out, rows.Err()
}

const webhookCols = `id, url, secret, events, enabled, created_at,
	last_status, last_attempt_at, last_error`

// ListWebhooks returns every configured endpoint, newest first.
func (s *Store) ListWebhooks() ([]model.Webhook, error) {
	return s.scanWebhooks(`SELECT ` + webhookCols + ` FROM webhooks ORDER BY id DESC`)
}

// GetWebhook returns one endpoint by id.
func (s *Store) GetWebhook(id int64) (*model.Webhook, error) {
	hooks, err := s.scanWebhooks(`SELECT `+webhookCols+` FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(hooks) == 0 {
		return nil, sql.ErrNoRows
	}
	return &hooks[0], nil
}

// EnabledWebhooksForEvent returns the enabled endpoints subscribed to event —
// the dispatcher's fan-out set. Subscription is filtered in Go (the events CSV is
// tiny) so "empty = all" stays in one place (model.Webhook.Subscribed).
func (s *Store) EnabledWebhooksForEvent(event string) ([]model.Webhook, error) {
	hooks, err := s.scanWebhooks(`SELECT ` + webhookCols + ` FROM webhooks WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	out := hooks[:0]
	for _, h := range hooks {
		if h.Subscribed(event) {
			out = append(out, h)
		}
	}
	return out, nil
}

// UpdateWebhook replaces the mutable fields (url, events, enabled) of an endpoint.
func (s *Store) UpdateWebhook(id int64, url string, events []string, enabled bool) error {
	_, err := s.db.Exec(
		`UPDATE webhooks SET url = ?, events = ?, enabled = ? WHERE id = ?`,
		url, joinEvents(events), boolToInt(enabled), id,
	)
	return err
}

// DeleteWebhook removes an endpoint.
func (s *Store) DeleteWebhook(id int64) error {
	_, err := s.db.Exec(`DELETE FROM webhooks WHERE id = ?`, id)
	return err
}

// MarkWebhookResult records the outcome of a delivery attempt (status is the HTTP
// code, 0 on a connection-level failure; errStr is "" on success).
func (s *Store) MarkWebhookResult(id int64, status int, errStr string) error {
	_, err := s.db.Exec(
		`UPDATE webhooks SET last_status = ?, last_attempt_at = ?, last_error = ? WHERE id = ?`,
		status, time.Now().Unix(), errStr, id,
	)
	return err
}
