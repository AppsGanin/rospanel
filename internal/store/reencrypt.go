package store

import (
	"encoding/json"
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

	var proxyAccs string
	if err := s.db.QueryRow(`SELECT proxy_accounts FROM settings WHERE id = 1`).Scan(&proxyAccs); err == nil && proxyAccs != "" && !strings.Contains(proxyAccs, "enc:v1:") {
		var accs []model.SystemProxyAccount
		if err := json.Unmarshal([]byte(proxyAccs), &accs); err == nil && len(accs) > 0 {
			if encAccs, err := encodeProxyAccounts(accs); err == nil && encAccs != proxyAccs {
				if _, err := s.db.Exec(`UPDATE settings SET proxy_accounts = ? WHERE id = 1`, encAccs); err != nil {
					return err
				}
			}
		}
	}

	if err := s.reencryptAdminTOTP(); err != nil {
		return err
	}
	if err := s.reencryptPaymentProviders(); err != nil {
		return err
	}
	if err := s.reencryptNodes(); err != nil {
		return err
	}
	if err := s.reencryptWebhooks(); err != nil {
		return err
	}
	return s.reencryptInbounds()
}

// reencryptAdminTOTP wraps any second-factor seed still stored as plaintext (a row
// written before the field was encrypted, or restored from an old backup).
func (s *Store) reencryptAdminTOTP() error {
	type row struct {
		id      int64
		secret  string
		pending string
	}
	var rows []row
	res, err := s.db.Query(`SELECT id, totp_secret, totp_pending FROM admins WHERE totp_secret <> '' OR totp_pending <> ''`)
	if err != nil {
		return err
	}
	for res.Next() {
		var r row
		if err := res.Scan(&r.id, &r.secret, &r.pending); err != nil {
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
		secEnc := r.secret
		if r.secret != "" && !strings.HasPrefix(r.secret, "enc:v1:") {
			enc := encField(r.secret)
			if secretRoundtripOK(enc) {
				secEnc = enc
			} else {
				log.Printf("[ERROR] reencrypt: admin %d totp secret roundtrip failed — leaving plaintext", r.id)
			}
		}
		pendEnc := r.pending
		if r.pending != "" && !strings.HasPrefix(r.pending, "enc:v1:") {
			enc := encField(r.pending)
			if secretRoundtripOK(enc) {
				pendEnc = enc
			} else {
				log.Printf("[ERROR] reencrypt: admin %d totp pending roundtrip failed — leaving plaintext", r.id)
			}
		}
		if secEnc != r.secret || pendEnc != r.pending {
			if _, err := s.db.Exec(`UPDATE admins SET totp_secret = ?, totp_pending = ? WHERE id = ?`, secEnc, pendEnc, r.id); err != nil {
				return err
			}
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

func (s *Store) reencryptNodes() error {
	type nodeRow struct {
		id            int64
		realityPriv   string
		warpPriv      string
		zerosslHMAC   string
		proxyAccounts string
	}
	var rows []nodeRow
	res, err := s.db.Query(`SELECT id, reality_private_key, warp_private_key, zerossl_eab_hmac, proxy_accounts FROM nodes`)
	if err != nil {
		return err
	}
	for res.Next() {
		var r nodeRow
		if err := res.Scan(&r.id, &r.realityPriv, &r.warpPriv, &r.zerosslHMAC, &r.proxyAccounts); err != nil {
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
		realityPriv := r.realityPriv
		if realityPriv != "" && !strings.HasPrefix(realityPriv, "enc:v1:") {
			enc := encField(realityPriv)
			if secretRoundtripOK(enc) {
				realityPriv = enc
			}
		}
		warpPriv := r.warpPriv
		if warpPriv != "" && !strings.HasPrefix(warpPriv, "enc:v1:") {
			enc := encField(warpPriv)
			if secretRoundtripOK(enc) {
				warpPriv = enc
			}
		}
		zerosslHMAC := r.zerosslHMAC
		if zerosslHMAC != "" && !strings.HasPrefix(zerosslHMAC, "enc:v1:") {
			enc := encField(zerosslHMAC)
			if secretRoundtripOK(enc) {
				zerosslHMAC = enc
			}
		}
		proxyAccounts := r.proxyAccounts
		if proxyAccounts != "" && !strings.Contains(proxyAccounts, "enc:v1:") {
			var accs []model.SystemProxyAccount
			if err := json.Unmarshal([]byte(proxyAccounts), &accs); err == nil && len(accs) > 0 {
				encAccs, err := encodeProxyAccounts(accs)
				if err == nil {
					proxyAccounts = encAccs
				}
			}
		}
		if realityPriv != r.realityPriv || warpPriv != r.warpPriv || zerosslHMAC != r.zerosslHMAC || proxyAccounts != r.proxyAccounts {
			if _, err := s.db.Exec(`UPDATE nodes SET reality_private_key = ?, warp_private_key = ?, zerossl_eab_hmac = ?, proxy_accounts = ? WHERE id = ?`,
				realityPriv, warpPriv, zerosslHMAC, proxyAccounts, r.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) reencryptWebhooks() error {
	type row struct {
		id     int64
		secret string
	}
	var rows []row
	res, err := s.db.Query(`SELECT id, secret FROM webhooks WHERE secret <> ''`)
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
			log.Printf("[ERROR] reencrypt: webhook %d secret roundtrip failed — leaving plaintext", r.id)
			continue
		}
		if _, err := s.db.Exec(`UPDATE webhooks SET secret = ? WHERE id = ?`, enc, r.id); err != nil {
			return err
		}
	}
	return nil
}

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
			continue
		}
		if opts.RealityPrivateKey == "" || strings.HasPrefix(opts.RealityPrivateKey, "enc:v1:") {
			continue
		}
		enc, err := marshalInboundOpts(opts)
		if err != nil {
			continue
		}
		if _, err := s.db.Exec(`UPDATE inbounds SET opts = ? WHERE id = ?`, enc, r.id); err != nil {
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
