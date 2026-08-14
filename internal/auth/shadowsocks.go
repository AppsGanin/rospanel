package auth

import (
	"crypto/rand"
	"encoding/base64"
)

// RandomShadowKey returns a Shadowsocks-2022 server key: n random bytes, standard
// base64 (the encoding Xray and every client expect for the psk). n is the method's
// key size — 16 for aes-128, 32 for aes-256 and chacha20 (see model.SSKeyLen). The
// per-user key is NOT generated here; it is derived from the user's UUID at render
// time (model.UserShadowKey) so it needs no storage of its own.
func RandomShadowKey(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
