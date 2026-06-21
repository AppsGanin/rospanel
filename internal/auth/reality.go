package auth

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// GenerateRealityKeys returns an X25519 keypair encoded base64 raw-url, matching
// the format of `xray x25519`: the private key goes into the server config, the
// public key (pbk=) into client share links.
func GenerateRealityKeys() (priv, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(k.Bytes()), enc.EncodeToString(k.PublicKey().Bytes()), nil
}

// RandomShortIDs returns a comma-separated set of REALITY shortIds of varying
// even hex lengths. The server accepts any of them; share links use the first
// (an 8-hex-char id, the widely-compatible default).
func RandomShortIDs() (string, error) {
	lengths := []int{8, 16, 12, 4, 6, 14, 10, 2}
	ids := make([]string, 0, len(lengths))
	for _, n := range lengths {
		b := make([]byte, n/2)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		ids = append(ids, hex.EncodeToString(b))
	}
	return strings.Join(ids, ","), nil
}

// RandomServiceName returns a random lowercase gRPC service name.
func RandomServiceName() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(secretPathEnc.EncodeToString(b)), nil
}
