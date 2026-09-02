package store

import (
	"encoding/json"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The source policy's record of what it refused (migration 0063). The kernel set is
// the enforcement; this table is what the operator reads, what survives a restart,
// and what the nodes are handed so one refusal covers the whole fleet.

// SetConnPolicy persists the policy as one JSON blob. Callers validate first.
func (s *Store) SetConnPolicy(p model.ConnPolicy) error {
	b, err := json.Marshal(p.Normalized())
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE settings SET conn_policy = ?, updated_at = unixepoch() WHERE id = 1`, string(b))
	return err
}

// BlockIP records a refused address, or extends one already recorded. The newest
// verdict wins: an address refused again for a different reason should read as the
// reason it was last refused for, not the first.
func (s *Store) BlockIP(b model.BlockedIP) error {
	_, err := s.db.Exec(`
		INSERT INTO blocked_ips (ip, reason, country, asn, org, user_id, at, until)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (ip) DO UPDATE SET
			reason = excluded.reason, country = excluded.country, asn = excluded.asn,
			org = excluded.org, user_id = excluded.user_id, until = excluded.until`,
		b.IP, b.Reason, b.Country, b.ASN, b.Org, b.UserID, b.At, b.Until)
	return err
}

// UnblockIP lifts a block by hand (the operator disagreeing with the policy).
// Reports whether a row went, so the caller can say "there was nothing to lift".
func (s *Store) UnblockIP(ip string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM blocked_ips WHERE ip = ?`, ip)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ClearBlockedIPs empties the table — what switching enforcement off means: nothing
// the policy dropped stays dropped.
func (s *Store) ClearBlockedIPs() error {
	_, err := s.db.Exec(`DELETE FROM blocked_ips`)
	return err
}

// ListBlockedIPs returns the live blocks, most recent first.
func (s *Store) ListBlockedIPs(limit int) ([]model.BlockedIP, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT ip, reason, country, asn, org, user_id, at, until
		FROM blocked_ips WHERE until > ? ORDER BY at DESC LIMIT ?`, time.Now().Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.BlockedIP{}
	for rows.Next() {
		var b model.BlockedIP
		if err := rows.Scan(&b.IP, &b.Reason, &b.Country, &b.ASN, &b.Org, &b.UserID, &b.At, &b.Until); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BlockedIPList is just the addresses still in force — what a node is handed.
func (s *Store) BlockedIPList() ([]string, error) {
	rows, err := s.db.Query(`SELECT ip FROM blocked_ips WHERE until > ? ORDER BY ip`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, err
		}
		out = append(out, ip)
	}
	return out, rows.Err()
}

// PurgeBlockedIPs drops rows whose block has lapsed. The kernel expires its own
// elements; this keeps the table (and what the nodes are handed) in step.
func (s *Store) PurgeBlockedIPs(now int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM blocked_ips WHERE until <= ?`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
