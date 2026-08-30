package happ

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/Shu1t3/rospanel-shu1t3/internal/netguard"
)

// Decrypt decodes a happ://crypt* deep link and returns the plaintext payload
// (typically a newline-separated list of proxy URIs).
//
// Supported formats:
//   - happ://crypt/…    — RSA-1024 PKCS1v15
//   - happ://crypt2/…   — RSA-4096 PKCS1v15
//   - happ://crypt3/…   — RSA-4096 PKCS1v15
//   - happ://crypt4/…   — RSA-4096 PKCS1v15
//   - happ://crypt5/…   — RSA PKCS1v15 + ChaCha20-Poly1305 (keytable)
func Decrypt(link string) ([]byte, error) {
	link = strings.TrimSpace(link)

	switch {
	case strings.HasPrefix(link, "happ://crypt5/"):
		return decryptCrypt5(link[len("happ://crypt5/"):])
	case strings.HasPrefix(link, "happ://crypt4/"):
		return decryptCrypt1to4(link[len("happ://crypt4/"):], 3)
	case strings.HasPrefix(link, "happ://crypt3/"):
		return decryptCrypt1to4(link[len("happ://crypt3/"):], 2)
	case strings.HasPrefix(link, "happ://crypt2/"):
		return decryptCrypt1to4(link[len("happ://crypt2/"):], 1)
	case strings.HasPrefix(link, "happ://crypt/"):
		return decryptCrypt1to4(link[len("happ://crypt/"):], 0)
	default:
		return nil, fmt.Errorf("unsupported happ link format")
	}
}

// IsHappLink reports whether s is a happ://crypt* deep link.
func IsHappLink(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "happ://crypt")
}

// ── crypt1–4 ────────────────────────────────────────────────────────────────

// parsedKeys caches the decoded RSA private keys (lazily parsed on first use).
var (
	parsedKeysMu sync.Mutex
	parsedKeys   [4]*rsa.PrivateKey
)

func getRSAKey(index int) (*rsa.PrivateKey, error) {
	parsedKeysMu.Lock()
	defer parsedKeysMu.Unlock()
	if parsedKeys[index] != nil {
		return parsedKeys[index], nil
	}
	der, err := base64.StdEncoding.DecodeString(pkcs1KeysB64[index])
	if err != nil {
		return nil, fmt.Errorf("crypt%d key base64: %w", index+1, err)
	}
	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("crypt%d key parse: %w", index+1, err)
	}
	parsedKeys[index] = key
	return key, nil
}

func decryptCrypt1to4(payload string, keyIndex int) ([]byte, error) {
	key, err := getRSAKey(keyIndex)
	if err != nil {
		return nil, err
	}
	ciphertext, err := decodeHappBase64(payload)
	if err != nil {
		return nil, fmt.Errorf("crypt%d base64: %w", keyIndex+1, err)
	}
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, key, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("crypt%d RSA decrypt: %w", keyIndex+1, err)
	}
	return plain, nil
}

// ── crypt5 ──────────────────────────────────────────────────────────────────

// crypt5Keytable is the lazily-loaded 36-entry marker → PKCS#8 key map.
var (
	crypt5KeytableMu sync.Mutex
	crypt5Keytable   map[string]string // marker → base64 PKCS#8 private key
)

func loadCrypt5Keytable() (map[string]string, error) {
	crypt5KeytableMu.Lock()
	defer crypt5KeytableMu.Unlock()
	if crypt5Keytable != nil {
		return crypt5Keytable, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body, err := netguard.Get(ctx, crypt5KeysURL, 512*1024)
	if err != nil {
		return nil, fmt.Errorf("crypt5 keytable fetch: %w", err)
	}
	var table map[string]string
	if err := json.Unmarshal(body, &table); err != nil {
		return nil, fmt.Errorf("crypt5 keytable parse: %w", err)
	}
	crypt5Keytable = table
	return table, nil
}

// decryptCrypt5 implements the direct-decrypt path from crypt5.js:
//
//	payload = base64(marker[4] | rsaBlock | chacha20poly1305(plaintext, key=rsa_decrypt(rsaBlock)))
//
// Some payloads use swap transforms (swapAdjacent, swapBlockHalves) before the RSA block.
// We try the canonical layout first; if ChaCha20 auth fails we try swap variants.
func decryptCrypt5(payload string) ([]byte, error) {
	data, err := decodeHappBase64(payload)
	if err != nil {
		return nil, fmt.Errorf("crypt5 base64: %w", err)
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("crypt5: payload too short")
	}

	marker := string(data[:4])
	rest := data[4:]

	keytable, err := loadCrypt5Keytable()
	if err != nil {
		return nil, err
	}
	keyB64, ok := keytable[marker]
	if !ok {
		return nil, fmt.Errorf("crypt5: unknown marker %q", marker)
	}
	privKey, err := parsePKCS8Key(keyB64)
	if err != nil {
		return nil, fmt.Errorf("crypt5 key parse: %w", err)
	}

	// Try layouts: canonical, swapAdjacent, swapBlockHalves, both swaps.
	layouts := []func([]byte) []byte{
		func(b []byte) []byte { return b },
		swapAdjacent,
		swapBlockHalves,
		func(b []byte) []byte { return swapBlockHalves(swapAdjacent(b)) },
	}
	for _, transform := range layouts {
		transformed := transform(rest)
		rsaSize := privKey.Size()
		if len(transformed) < rsaSize {
			continue
		}
		rsaBlock := transformed[:rsaSize]
		chachaData := transformed[rsaSize:]
		symKey, err := rsa.DecryptPKCS1v15(rand.Reader, privKey, rsaBlock)
		if err != nil || len(symKey) < chacha20poly1305.KeySize+chacha20poly1305.NonceSize {
			continue
		}
		key := symKey[:chacha20poly1305.KeySize]
		nonce := symKey[chacha20poly1305.KeySize : chacha20poly1305.KeySize+chacha20poly1305.NonceSize]
		aead, err := chacha20poly1305.New(key)
		if err != nil {
			continue
		}
		plain, err := aead.Open(nil, nonce, chachaData, nil)
		if err == nil {
			return plain, nil
		}
	}
	return nil, fmt.Errorf("crypt5: decryption failed for marker %q", marker)
}

func parsePKCS8Key(b64 string) (*rsa.PrivateKey, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// Try URL-safe base64
		der, err = base64.URLEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("PKCS8 base64: %w", err)
		}
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("PKCS8 parse: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("PKCS8: not an RSA key")
	}
	return rsaKey, nil
}

// ── byte transforms (ported from crypt5.js) ─────────────────────────────────

// swapAdjacent swaps every pair of adjacent bytes: [A B C D …] → [B A D C …].
// Odd trailing bytes are left unchanged. This transform is its own inverse.
func swapAdjacent(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	for i := 0; i+1 < len(out); i += 2 {
		out[i], out[i+1] = out[i+1], out[i]
	}
	return out
}

// swapBlockHalves performs ABCD → CDAB for every complete four-byte block.
// This transform is its own inverse.
func swapBlockHalves(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	full := len(out) - (len(out) % 4)
	for i := 0; i < full; i += 4 {
		out[i], out[i+2] = out[i+2], out[i]
		out[i+1], out[i+3] = out[i+3], out[i+1]
	}
	return out
}

// ── base64 helper ────────────────────────────────────────────────────────────

// decodeHappBase64 decodes a Happ-flavoured base64 payload: URL-safe characters
// are normalised, whitespace stripped, and missing padding restored.
func decodeHappBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	s = strings.TrimRight(s, "=")
	for len(s)%4 != 0 {
		s += "="
	}
	return base64.StdEncoding.DecodeString(s)
}
