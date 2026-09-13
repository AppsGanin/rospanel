package store

import (
	"encoding/json"
)

// TrustedNets returns the stored list of networks the panel never bans on its own
// (migration 0073), as it was normalized on save. A blank or unreadable column is
// the empty list: trusting nobody is how every ban behaved before the list existed.
func (s *Store) TrustedNets() ([]string, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT trusted_nets FROM settings WHERE id = 1`).Scan(&raw); err != nil {
		return nil, err
	}
	out := []string{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
			return []string{}, nil
		}
	}
	return out, nil
}

// SetTrustedNets stores the list. Callers normalize first.
func (s *Store) SetTrustedNets(nets []string) error {
	if nets == nil {
		nets = []string{}
	}
	b, err := json.Marshal(nets)
	if err != nil {
		return err
	}
	return s.setSetting("trusted_nets", string(b))
}
