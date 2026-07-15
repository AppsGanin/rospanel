package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Payment-provider credentials. One row per provider (registry key), config being
// that provider's declared fields as a JSON object. The whole object is encrypted
// at rest — it holds API keys and secret words, and which fields are secret is the
// registry's business, not the store's.

// ListPaymentProviders returns every saved provider row, keyed by provider key.
// Providers the operator never touched simply have no row.
func (s *Store) ListPaymentProviders() (map[string]model.PaymentProvider, error) {
	rows, err := s.db.Query(`SELECT key, enabled, config FROM payment_providers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.PaymentProvider{}
	for rows.Next() {
		var key, cfg string
		var enabled int
		if err := rows.Scan(&key, &enabled, &cfg); err != nil {
			return nil, err
		}
		out[key] = model.PaymentProvider{
			Key:     key,
			Enabled: enabled != 0,
			Config:  decodeProviderConfig(cfg),
		}
	}
	return out, rows.Err()
}

// GetPaymentProvider returns one provider's row. A provider that was never saved
// comes back as a zero-value row (disabled, empty config) — not an error.
func (s *Store) GetPaymentProvider(key string) (model.PaymentProvider, error) {
	var cfg string
	var enabled int
	err := s.db.QueryRow(`SELECT enabled, config FROM payment_providers WHERE key = ?`, key).Scan(&enabled, &cfg)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PaymentProvider{Key: key, Config: map[string]string{}}, nil
	}
	if err != nil {
		return model.PaymentProvider{}, err
	}
	return model.PaymentProvider{Key: key, Enabled: enabled != 0, Config: decodeProviderConfig(cfg)}, nil
}

// SavePaymentProvider upserts a provider's row. Config is stored encrypted.
func (s *Store) SavePaymentProvider(p model.PaymentProvider) error {
	raw, err := json.Marshal(p.Config)
	if err != nil {
		return err
	}
	var enabled int
	if p.Enabled {
		enabled = 1
	}
	_, err = s.db.Exec(`
		INSERT INTO payment_providers (key, enabled, config) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET enabled = excluded.enabled, config = excluded.config`,
		p.Key, enabled, encField(string(raw)))
	return err
}

func decodeProviderConfig(blob string) map[string]string {
	cfg := map[string]string{}
	if blob == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(decField(blob)), &cfg); err != nil {
		return map[string]string{}
	}
	return cfg
}
