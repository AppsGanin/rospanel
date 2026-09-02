package extsub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/AppsGanin/rospanel/internal/netguard"
)

// Happ hands its subscriptions out as happ://crypt… deep links: the server list,
// encrypted to a key the app carries. The formats and keys are documented by the
// happ-decryptor project (see happ_keys.go); this is a port of what that
// documentation describes, and nothing more — crypt through crypt4 are one RSA
// block each, crypt5 wraps a ChaCha20-Poly1305 payload in an RSA-encrypted key.

// IsHappLink reports whether s is a happ://crypt… deep link.
func IsHappLink(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "happ://crypt")
}

// DecryptHapp turns a happ://crypt… link into the plaintext it carries — normally
// a newline-separated list of share links.
func DecryptHapp(link string) ([]byte, error) {
	link = strings.TrimSpace(link)
	for i, prefix := range []string{"happ://crypt/", "happ://crypt2/", "happ://crypt3/", "happ://crypt4/"} {
		if rest, ok := strings.CutPrefix(link, prefix); ok {
			return decryptRSA(rest, i)
		}
	}
	if rest, ok := strings.CutPrefix(link, "happ://crypt5/"); ok {
		return decryptCrypt5(rest)
	}
	return nil, errors.New("unsupported happ link format")
}

// ── crypt … crypt4: one RSA PKCS#1 v1.5 block ─────────────────────────────

var (
	rsaKeysMu sync.Mutex
	rsaKeys   [4]*rsa.PrivateKey
)

func happRSAKey(i int) (*rsa.PrivateKey, error) {
	rsaKeysMu.Lock()
	defer rsaKeysMu.Unlock()
	if rsaKeys[i] != nil {
		return rsaKeys[i], nil
	}
	der, err := base64.StdEncoding.DecodeString(happRSAKeys[i])
	if err != nil {
		return nil, fmt.Errorf("happ key %d: %w", i+1, err)
	}
	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("happ key %d: %w", i+1, err)
	}
	rsaKeys[i] = key
	return key, nil
}

func decryptRSA(payload string, keyIndex int) ([]byte, error) {
	key, err := happRSAKey(keyIndex)
	if err != nil {
		return nil, err
	}
	ciphertext, err := happBase64(payload)
	if err != nil {
		return nil, fmt.Errorf("happ crypt%d: %w", keyIndex+1, err)
	}
	// PKCS#1 v1.5 is what the app uses; the panel has no say in the scheme. The
	// padding-oracle concern the deprecation names assumes an attacker who can
	// submit ciphertexts and observe failures — here the only ciphertexts decrypted
	// are links the operator chose to import, with keys that are public anyway.
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, key, ciphertext) //nolint:staticcheck // Happ's scheme, see above
	if err != nil {
		return nil, fmt.Errorf("happ crypt%d: %w", keyIndex+1, err)
	}
	return plain, nil
}

// ── crypt5: marker | RSA(key‖nonce) | ChaCha20-Poly1305(payload) ───────────

var (
	crypt5Mu   sync.Mutex
	crypt5Keys map[string]string // marker → PKCS#8 private key, base64
)

// crypt5Keytable fetches the marker→key table once per process. It is the one
// network dependency of the whole scheme; a fetch failure is reported as such
// rather than cached, so the next link tries again.
func crypt5Keytable(ctx context.Context) (map[string]string, error) {
	crypt5Mu.Lock()
	defer crypt5Mu.Unlock()
	if crypt5Keys != nil {
		return crypt5Keys, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body, err := netguard.Get(ctx, happCrypt5KeysURL, 512*1024)
	if err != nil {
		return nil, fmt.Errorf("happ crypt5 key table: %w", err)
	}
	var table map[string]string
	if err := json.Unmarshal(body, &table); err != nil || len(table) == 0 {
		return nil, errors.New("happ crypt5 key table: not a marker→key map")
	}
	crypt5Keys = table
	return table, nil
}

func decryptCrypt5(payload string) ([]byte, error) {
	data, err := happBase64(payload)
	if err != nil {
		return nil, fmt.Errorf("happ crypt5: %w", err)
	}
	if len(data) < 4 {
		return nil, errors.New("happ crypt5: payload too short")
	}
	marker, rest := string(data[:4]), data[4:]
	table, err := crypt5Keytable(context.Background())
	if err != nil {
		return nil, err
	}
	keyB64, ok := table[marker]
	if !ok {
		return nil, fmt.Errorf("happ crypt5: unknown marker %q", marker)
	}
	key, err := pkcs8RSA(keyB64)
	if err != nil {
		return nil, fmt.Errorf("happ crypt5: %w", err)
	}
	// The app scrambles some payloads with byte swaps before the RSA block; each
	// transform is its own inverse, so the candidates are simply tried in turn and
	// the AEAD tag says which one was used.
	layouts := []func([]byte) []byte{
		func(b []byte) []byte { return b },
		swapAdjacent,
		swapBlockHalves,
		func(b []byte) []byte { return swapBlockHalves(swapAdjacent(b)) },
	}
	for _, layout := range layouts {
		b := layout(rest)
		if len(b) < key.Size() {
			continue
		}
		secret, err := rsa.DecryptPKCS1v15(rand.Reader, key, b[:key.Size()]) //nolint:staticcheck // Happ's scheme, see decryptRSA
		if err != nil || len(secret) < chacha20poly1305.KeySize+chacha20poly1305.NonceSize {
			continue
		}
		aead, err := chacha20poly1305.New(secret[:chacha20poly1305.KeySize])
		if err != nil {
			continue
		}
		nonce := secret[chacha20poly1305.KeySize : chacha20poly1305.KeySize+chacha20poly1305.NonceSize]
		if plain, err := aead.Open(nil, nonce, b[key.Size():], nil); err == nil {
			return plain, nil
		}
	}
	return nil, fmt.Errorf("happ crypt5: no layout decrypts marker %q", marker)
}

func pkcs8RSA(b64 string) (*rsa.PrivateKey, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		if der, err = base64.URLEncoding.DecodeString(b64); err != nil {
			return nil, err
		}
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return rsaKey, nil
}

// swapAdjacent exchanges every pair of neighbouring bytes; a trailing odd byte
// stays. Its own inverse.
func swapAdjacent(b []byte) []byte {
	out := append([]byte(nil), b...)
	for i := 0; i+1 < len(out); i += 2 {
		out[i], out[i+1] = out[i+1], out[i]
	}
	return out
}

// swapBlockHalves turns ABCD into CDAB for every whole four-byte block. Its own
// inverse.
func swapBlockHalves(b []byte) []byte {
	out := append([]byte(nil), b...)
	for i := 0; i+3 < len(out); i += 4 {
		out[i], out[i+2] = out[i+2], out[i]
		out[i+1], out[i+3] = out[i+3], out[i+1]
	}
	return out
}

// happBase64 decodes the app's base64: URL-safe or standard alphabet, padding
// optional, whitespace tolerated.
func happBase64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		case '-':
			return '+'
		case '_':
			return '/'
		}
		return r
	}, s)
	s = strings.TrimRight(s, "=")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	return base64.StdEncoding.DecodeString(s)
}
