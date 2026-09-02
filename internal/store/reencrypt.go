package store

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
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
		{"zerossl_eab_hmac"}, {"awg_private_key"},
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
	for _, step := range []struct {
		what string
		fn   func() error
	}{
		{"admin second factors", s.reencryptAdminTOTP},
		{"pending second factors", s.reencryptAdminTOTPPending},
		{"payment providers", s.reencryptPaymentProviders},
		{"user tunnel keys", s.reencryptUserWGKeys},
		{"node keys", s.reencryptNodes},
		{"webhook secrets", s.reencryptWebhooks},
		{"custom inbound keys", s.reencryptInbounds},
		{"system proxy accounts", s.reencryptProxyAccounts},
	} {
		if err := step.fn(); err != nil {
			return fmt.Errorf("reencrypt %s: %w", step.what, err)
		}
	}
	return nil
}

// reencryptColumns is the shape every remaining sweep has: read (id, secret…) from
// one table, wrap whatever is still plaintext, write it back. Reading everything
// first is not an optimisation but a requirement — the store holds a single
// connection, so an Exec inside rows.Next deadlocks.
//
// A value that does not survive a decrypt round-trip is LEFT ALONE and logged: a
// panel that cannot read a secret back is a panel whose users cannot connect, and a
// plaintext row is recoverable where a corrupt blob is not.
func (s *Store) reencryptColumns(table, query string, cols []string, update string) error {
	type row struct {
		id   int64
		vals []string
	}
	var rows []row
	res, err := s.db.Query(query)
	if err != nil {
		return err
	}
	for res.Next() {
		r := row{vals: make([]string, len(cols))}
		dest := make([]any, 0, len(cols)+1)
		dest = append(dest, &r.id)
		for i := range r.vals {
			dest = append(dest, &r.vals[i])
		}
		if err := res.Scan(dest...); err != nil {
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
		changed := false
		args := make([]any, 0, len(r.vals)+1)
		for i, v := range r.vals {
			if v == "" || strings.HasPrefix(v, "enc:v1:") {
				args = append(args, v)
				continue
			}
			enc := encField(v)
			if !secretRoundtripOK(enc) {
				log.Printf("[ERROR] reencrypt: %s %d %s roundtrip failed — leaving plaintext", table, r.id, cols[i])
				args = append(args, v)
				continue
			}
			args = append(args, enc)
			changed = true
		}
		if !changed {
			continue
		}
		if _, err := s.db.Exec(update, append(args, r.id)...); err != nil {
			return err
		}
	}
	return nil
}

// reencryptUserWGKeys covers the AmneziaWG identity minted for a user.
func (s *Store) reencryptUserWGKeys() error {
	return s.reencryptColumns("user", `SELECT id, wg_private_key FROM users WHERE wg_private_key <> ''`,
		[]string{"wg_private_key"}, `UPDATE users SET wg_private_key = ? WHERE id = ?`)
}

// reencryptNodes covers a node's own key material: REALITY, WARP, AmneziaWG and the
// ZeroSSL EAB secret. A fleet restored from an old backup carries all four.
func (s *Store) reencryptNodes() error {
	return s.reencryptColumns("node",
		`SELECT id, reality_private_key, warp_private_key, awg_private_key, zerossl_eab_hmac FROM nodes`,
		[]string{"reality_private_key", "warp_private_key", "awg_private_key", "zerossl_eab_hmac"},
		`UPDATE nodes SET reality_private_key = ?, warp_private_key = ?, awg_private_key = ?, zerossl_eab_hmac = ? WHERE id = ?`)
}

// reencryptWebhooks covers the HMAC signing secret an integration verifies with.
func (s *Store) reencryptWebhooks() error {
	return s.reencryptColumns("webhook", `SELECT id, secret FROM webhooks WHERE secret <> ''`,
		[]string{"secret"}, `UPDATE webhooks SET secret = ? WHERE id = ?`)
}

// reencryptAdminTOTPPending covers the seed of an enrolment that was started and not
// confirmed — the same secret as a live one until it is.
func (s *Store) reencryptAdminTOTPPending() error {
	return s.reencryptColumns("admin", `SELECT id, totp_pending FROM admins WHERE totp_pending <> ''`,
		[]string{"totp_pending"}, `UPDATE admins SET totp_pending = ? WHERE id = ?`)
}

// reencryptInbounds covers the REALITY private key inside a custom inbound's opts
// blob. The blob is JSON with one encrypted field, so it is decoded, re-encoded
// through the same marshaller the write path uses, and only written when it changed.
func (s *Store) reencryptInbounds() error {
	type row struct {
		id   int64
		opts string
	}
	var rows []row
	res, err := s.db.Query(`SELECT id, opts FROM inbounds WHERE opts <> ''`)
	if err != nil {
		return err
	}
	for res.Next() {
		var r row
		if err := res.Scan(&r.id, &r.opts); err != nil {
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
		var opts model.InboundOpts
		if err := json.Unmarshal([]byte(r.opts), &opts); err != nil {
			log.Printf("[ERROR] reencrypt: inbound %d opts unreadable — leaving as is", r.id)
			continue
		}
		if opts.RealityPrivateKey == "" || strings.HasPrefix(opts.RealityPrivateKey, "enc:v1:") {
			continue
		}
		blob, err := marshalInboundOpts(opts)
		if err != nil {
			log.Printf("[ERROR] reencrypt: inbound %d opts re-encode failed — leaving as is", r.id)
			continue
		}
		if _, err := s.db.Exec(`UPDATE inbounds SET opts = ? WHERE id = ?`, blob, r.id); err != nil {
			return err
		}
	}
	return nil
}

// reencryptProxyAccounts covers the system proxy's passwords, which live as one JSON
// array with each password wrapped on its own (see encodeProxyAccounts).
func (s *Store) reencryptProxyAccounts() error {
	var raw string
	if err := s.db.QueryRow(`SELECT proxy_accounts FROM settings WHERE id = 1`).Scan(&raw); err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	var accs []model.SystemProxyAccount
	if err := json.Unmarshal([]byte(raw), &accs); err != nil {
		log.Printf("[ERROR] reencrypt: system proxy accounts unreadable — leaving as is")
		return nil
	}
	plain := false
	for _, a := range accs {
		if a.Pass != "" && !strings.HasPrefix(a.Pass, "enc:v1:") {
			plain = true
		}
	}
	if !plain {
		return nil
	}
	// decodeProxyAccounts leaves an already-encrypted password decrypted and a
	// plaintext one as it is, so re-encoding wraps exactly what was bare.
	blob, err := encodeProxyAccounts(decodeProxyAccounts(raw))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE settings SET proxy_accounts = ? WHERE id = 1`, blob)
	return err
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
