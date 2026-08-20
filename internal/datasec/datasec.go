// Package datasec provides at-rest encryption for secrets stored in SQLite.
//
// The key lives in dataDir/secrets.key (mode 0600) and IS included in backup
// archives — the encrypted DB is useless without it, so a backup that left it out
// would not restore to a working panel on a fresh host. The trade-off is that a
// backup archive is as sensitive as the key itself: treat it accordingly.
package datasec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	keyFile   = "secrets.key"
	keySize   = 32
	encPrefix = "enc:v1:"
)

var key []byte

// Init loads or creates the per-install encryption key. Call once before opening the store.
func Init(dataDir string) error {
	path := filepath.Join(dataDir, keyFile)
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) != keySize {
			return errors.New("secrets.key: wrong size")
		}
		key = b
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	dbPath := filepath.Join(dataDir, "rospanel.db")
	if enc, err := dbHasEncryptedSecrets(dbPath); err != nil {
		return err
	} else if enc {
		return fmt.Errorf(
			"secrets.key is missing but %s already holds encrypted secrets — "+
				"restore secrets.key from a backup of the data directory",
			dbPath,
		)
	}
	k := make([]byte, keySize)
	if _, err := rand.Read(k); err != nil {
		return err
	}
	if err := os.WriteFile(path, k, 0o600); err != nil {
		return err
	}
	key = k
	return nil
}

func dbHasEncryptedSecrets(dbPath string) (bool, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return false, err
	}
	defer db.Close()
	// Every encrypted column belongs here, not just the ones an install usually has.
	// This is the guard that tells "no key yet, fresh install" apart from "the key is
	// gone" — miss a column and an install configured with only that one secret looks
	// fresh, so a new key is minted and the old ciphertext becomes unreadable for
	// good. tg_support_bot_token was exactly that case.
	checks := []string{
		`SELECT tg_bot_token FROM settings WHERE id = 1`,
		`SELECT tg_user_bot_token FROM settings WHERE id = 1`,
		`SELECT tg_support_bot_token FROM settings WHERE id = 1`,
		`SELECT reality_private_key FROM settings WHERE id = 1`,
		`SELECT warp_private_key FROM settings WHERE id = 1`,
		`SELECT proxy_accounts FROM settings WHERE proxy_accounts LIKE '%enc:v1:%' AND id = 1`,
		`SELECT zerossl_eab_hmac FROM settings WHERE id = 1`,
		`SELECT password FROM users WHERE password LIKE 'enc:v1:%' LIMIT 1`,
		`SELECT reality_private_key FROM nodes WHERE reality_private_key LIKE 'enc:v1:%' LIMIT 1`,
		`SELECT warp_private_key FROM nodes WHERE warp_private_key LIKE 'enc:v1:%' LIMIT 1`,
		`SELECT zerossl_eab_hmac FROM nodes WHERE zerossl_eab_hmac LIKE 'enc:v1:%' LIMIT 1`,
		`SELECT proxy_accounts FROM nodes WHERE proxy_accounts LIKE '%enc:v1:%' LIMIT 1`,
		`SELECT totp_secret FROM admins WHERE totp_secret LIKE 'enc:v1:%' LIMIT 1`,
	}
	for _, q := range checks {
		var v string
		if err := db.QueryRow(q).Scan(&v); err != nil {
			continue
		}
		// Contains, not HasPrefix: most of these columns ARE one ciphertext, but
		// proxy_accounts is a JSON array whose passwords each carry the envelope, so
		// there the marker sits inside the value. The question this guard asks is
		// "does anything stored here need the key", and that is true either way.
		if strings.Contains(v, encPrefix) {
			return true, nil
		}
	}
	return false, nil
}

// Derive returns a deterministic secret for label, bound to THIS install's key.
//
// The key lives in dataDir/secrets.key and is never sent anywhere, so a value derived
// here is stable across restarts (no config churn) yet cannot be reproduced by anyone
// holding only data the panel hands out. That is the difference that matters for
// placeholder credentials: hashing a value clients already possess makes the result a
// public formula, not a secret.
//
// ok is false when the install has no key (encryption disabled); the caller must then
// decide for itself, because there is nothing here to keep a secret with.
func Derive(label string) ([]byte, bool) {
	if key == nil {
		return nil, false
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(label))
	return m.Sum(nil), true
}

// Encrypt returns s unchanged when empty; otherwise an enc:v1:… blob.
func Encrypt(s string) (string, error) {
	if s == "" || key == nil {
		return s, nil
	}
	if strings.HasPrefix(s, encPrefix) {
		return s, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(s), nil)
	return encPrefix + base64.RawStdEncoding.EncodeToString(out), nil
}

// Decrypt returns plaintext; values without enc:v1: pass through (legacy rows).
func Decrypt(s string) (string, error) {
	if s == "" || !strings.HasPrefix(s, encPrefix) || key == nil {
		return s, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(s, encPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
