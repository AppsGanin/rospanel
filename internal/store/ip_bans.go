package store

import "github.com/AppsGanin/rospanel/internal/model"

// Addresses banned by hand (migration 0082). The kernel set is the enforcement; this
// table is what survives a restart, what the operator reads, and what the nodes are
// handed.

// BanIP records a ban. Banning an address already banned keeps the first ban's time
// and takes the newest user it was banned from.
func (s *Store) BanIP(b model.IPBan) error {
	_, err := s.db.Exec(`
		INSERT INTO ip_bans (ip, user_id, at) VALUES (?, ?, ?)
		ON CONFLICT (ip) DO UPDATE SET user_id = excluded.user_id`,
		b.IP, b.UserID, b.At)
	return err
}

// UnbanIP lifts a ban and reports whether there was one.
func (s *Store) UnbanIP(ip string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM ip_bans WHERE ip = ?`, ip)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListIPBans returns every ban, newest first.
func (s *Store) ListIPBans() ([]model.IPBan, error) {
	rows, err := s.db.Query(`SELECT ip, user_id, at FROM ip_bans ORDER BY at DESC, ip`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.IPBan{}
	for rows.Next() {
		var b model.IPBan
		if err := rows.Scan(&b.IP, &b.UserID, &b.At); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BannedIPList is just the banned addresses, in address order — what a node is handed.
func (s *Store) BannedIPList() ([]string, error) {
	rows, err := s.db.Query(`SELECT ip FROM ip_bans ORDER BY ip`)
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
