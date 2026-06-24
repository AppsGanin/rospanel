// Package datasec provides at-rest encryption for secrets stored in SQLite.
// The key lives in dataDir/secrets.key (mode 0600) and is excluded from backups.
package datasec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
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
			return errors.New("secrets.key: неверный размер")
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
			"secrets.key отсутствует, но в %s уже есть зашифрованные секреты — "+
				"восстановите secrets.key из резервной копии каталога данных (файл не входит в tar-бэкап)",
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
	checks := []string{
		`SELECT tg_bot_token FROM settings WHERE id = 1`,
		`SELECT tg_user_bot_token FROM settings WHERE id = 1`,
		`SELECT reality_private_key FROM settings WHERE id = 1`,
		`SELECT password FROM users WHERE password LIKE 'enc:v1:%' LIMIT 1`,
	}
	for _, q := range checks {
		var v string
		if err := db.QueryRow(q).Scan(&v); err != nil {
			continue
		}
		if strings.HasPrefix(v, encPrefix) {
			return true, nil
		}
	}
	return false, nil
}

// KeyFileName is the basename excluded from backup archives.
func KeyFileName() string { return keyFile }

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
		return "", errors.New("неверный шифротекст")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// EncryptFile reads path and returns encrypted bytes (AES-GCM, random nonce prepended).
func EncryptFile(path string) ([]byte, error) {
	if key == nil {
		return nil, errors.New("datasec: не инициализирован")
	}
	plain, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}
