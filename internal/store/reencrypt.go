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
		{"tg_bot_token"}, {"tg_user_bot_token"}, {"tg_support_bot_token"}, {"tg_proxy"},
		{"warp_private_key"}, {"reality_private_key"},
		{"zerossl_eab_hmac"},
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
	if err := s.reencryptAdminTOTP(); err != nil {
		return err
	}
	return s.reencryptPaymentProviders()
}

// reencryptAdminTOTP wraps any second-factor seed still stored as plaintext (a row
// written before the field was encrypted, or restored from an old backup).
func (s *Store) reencryptAdminTOTP() error {
	type row struct {
		id     int64
		secret string
	}
	var rows []row
	res, err := s.db.Query(`SELECT id, totp_secret FROM admins WHERE totp_secret <> ''`)
	if err != nil {
		return err
	}
	for res.Next() {
		var r row
		if err := res.Scan(&r.id, &r.secret); err != nil {
			res.Close()
			return err
		}
		rows = append(rows, r)
	}
	if err := res.Close(); err != nil {
		return err
	}
	if err := res.Err(); err != nil {
		return err
	}
	for _, r := range rows {
		if strings.HasPrefix(r.secret, "enc:v1:") {
			continue
		}
		enc := encField(r.secret)
		if !secretRoundtripOK(enc) {
			log.Printf("[ERROR] reencrypt: admin %d totp secret roundtrip failed — leaving plaintext", r.id)
			continue
		}
		if _, err := s.db.Exec(`UPDATE admins SET totp_secret = ? WHERE id = ?`, enc, r.id); err != nil {
			return err
		}
	}
	return nil
}

// reencryptPaymentProviders wraps any provider config still stored as plaintext
// JSON (a row written before the field was encrypted, or restored from an old
// backup) in the at-rest envelope.
func (s *Store) reencryptPaymentProviders() error {
	type row struct{ key, config string }
	var rows []row
	res, err := s.db.Query(`SELECT key, config FROM payment_providers`)
	if err != nil {
		return err
	}
	for res.Next() {
		var r row
		if err := res.Scan(&r.key, &r.config); err != nil {
			res.Close()
			return err
		}
		rows = append(rows, r)
	}
	if err := res.Close(); err != nil {
		return err
	}
	if err := res.Err(); err != nil {
		return err
	}
	for _, r := range rows {
		if r.config == "" || strings.HasPrefix(r.config, "enc:v1:") {
			continue
		}
		enc := encField(r.config)
		if !secretRoundtripOK(enc) {
			log.Printf("[ERROR] reencrypt: payment_providers.%s roundtrip failed — leaving plaintext", r.key)
			continue
		}
		if _, err := s.db.Exec(`UPDATE payment_providers SET config = ? WHERE key = ?`, enc, r.key); err != nil {
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
