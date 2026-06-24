package store

import (
	"log"
	"strings"
)

type userSecretRow struct {
	id       int64
	password string
}

// ReencryptSensitiveFields migrates legacy plaintext secrets to enc:v1: at-rest blobs.
func (s *Store) ReencryptSensitiveFields() error {
	// Read all rows first — with MaxOpenConns(1), Exec inside rows.Next deadlocks.
	var users []userSecretRow
	rows, err := s.db.Query(`SELECT id, password FROM users`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var u userSecretRow
		if err := rows.Scan(&u.id, &u.password); err != nil {
			rows.Close()
			return err
		}
		users = append(users, u)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, u := range users {
		if u.password == "" || strings.HasPrefix(u.password, "enc:v1:") {
			continue
		}
		enc := encField(u.password)
		if !secretRoundtripOK(enc) {
			log.Printf("[ERROR] reencrypt: user %d password roundtrip failed — leaving plaintext", u.id)
			continue
		}
		if _, err := s.db.Exec(`UPDATE users SET password = ? WHERE id = ?`, enc, u.id); err != nil {
			return err
		}
	}

	type col struct {
		name string
	}
	for _, c := range []col{
		{"tg_bot_token"}, {"tg_user_bot_token"}, {"warp_private_key"},
		{"reality_private_key"}, {"proxy_mode_pass"}, {"zerossl_eab_hmac"},
	} {
		var val string
		if err := s.db.QueryRow(`SELECT ` + c.name + ` FROM settings WHERE id = 1`).Scan(&val); err != nil {
			return err
		}
		if val == "" || strings.HasPrefix(val, "enc:v1:") {
			continue
		}
		enc := encField(val)
		if !secretRoundtripOK(enc) {
			log.Printf("[ERROR] reencrypt: settings.%s roundtrip failed — leaving plaintext", c.name)
			continue
		}
		if _, err := s.db.Exec(`UPDATE settings SET `+c.name+` = ? WHERE id = 1`, enc); err != nil {
			return err
		}
	}
	return nil
}

func secretRoundtripOK(enc string) bool {
	if enc == "" || !strings.HasPrefix(enc, "enc:v1:") {
		return false
	}
	return decField(enc) != ""
}
