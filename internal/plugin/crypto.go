package plugin

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // payment providers sign with MD5; the plugin asks for it by name
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // likewise SHA-1
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math/big"

	"github.com/google/uuid"
)

// cryptoOp serves panel.crypto.*. QuickJS has no WebCrypto, and payment providers
// sign with HMAC, MD5/SHA and RSA; Google-style APIs want a signed JWT.
//
// Data and keys are strings: UTF-8 text, or {"base64": "…"} for bytes. Digests and
// signatures come back hex or base64 (signatures base64, JWT-style raw for ES256).
func cryptoOp(op string, arg []byte) (any, error) {
	switch op {
	case "crypto.hash":
		a, err := decode[struct {
			Alg  string    `json:"alg"`
			Data dataValue `json:"data"`
			Enc  string    `json:"enc"`
		}](arg)
		if err != nil {
			return nil, err
		}
		h, err := hasher(a.Alg)
		if err != nil {
			return nil, err
		}
		h.Write(a.Data)
		return encode(h.Sum(nil), a.Enc)
	case "crypto.hmac":
		a, err := decode[struct {
			Alg  string    `json:"alg"`
			Key  dataValue `json:"key"`
			Data dataValue `json:"data"`
			Enc  string    `json:"enc"`
		}](arg)
		if err != nil {
			return nil, err
		}
		if _, err := hasher(a.Alg); err != nil {
			return nil, err
		}
		m := hmac.New(func() hash.Hash { h, _ := hasher(a.Alg); return h }, a.Key)
		m.Write(a.Data)
		return encode(m.Sum(nil), a.Enc)
	case "crypto.sign":
		a, err := decode[struct {
			Alg  string    `json:"alg"`
			PEM  string    `json:"pem"`
			Data dataValue `json:"data"`
		}](arg)
		if err != nil {
			return nil, err
		}
		sig, err := sign(a.Alg, a.PEM, a.Data)
		if err != nil {
			return nil, err
		}
		return base64.StdEncoding.EncodeToString(sig), nil
	case "crypto.verify":
		a, err := decode[struct {
			Alg  string    `json:"alg"`
			PEM  string    `json:"pem"`
			Data dataValue `json:"data"`
			Sig  string    `json:"sig"`
		}](arg)
		if err != nil {
			return nil, err
		}
		sig, err := base64.StdEncoding.DecodeString(a.Sig)
		if err != nil {
			return nil, fmt.Errorf("signature: %w", err)
		}
		return verify(a.Alg, a.PEM, a.Data, sig)
	case "crypto.jwt":
		a, err := decode[struct {
			Alg    string         `json:"alg"`
			PEM    string         `json:"pem"`
			Claims map[string]any `json:"claims"`
			Header map[string]any `json:"header"`
		}](arg)
		if err != nil {
			return nil, err
		}
		return jwt(a.Alg, a.PEM, a.Claims, a.Header)
	case "crypto.random":
		a, err := decode[struct {
			N int `json:"n"`
		}](arg)
		if err != nil {
			return nil, err
		}
		if a.N < 1 || a.N > 1024 {
			return nil, errors.New("randomHex: 1-1024 bytes")
		}
		b := make([]byte, a.N)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b), nil
	case "crypto.uuid":
		return uuid.NewString(), nil
	}
	return nil, fmt.Errorf("unknown operation %q", op)
}

// dataValue is a string argument that may carry bytes as {"base64": "…"}.
type dataValue []byte

func (d *dataValue) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*d = []byte(s)
		return nil
	}
	var o struct {
		Base64 *string `json:"base64"`
	}
	if err := json.Unmarshal(b, &o); err != nil || o.Base64 == nil {
		return errors.New(`expected a string or {"base64": "…"}`)
	}
	raw, err := base64.StdEncoding.DecodeString(*o.Base64)
	if err != nil {
		return err
	}
	*d = raw
	return nil
}

func hasher(alg string) (hash.Hash, error) {
	switch alg {
	case "md5":
		return md5.New(), nil //nolint:gosec
	case "sha1":
		return sha1.New(), nil //nolint:gosec
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	}
	return nil, fmt.Errorf("hash %q: one of md5, sha1, sha256, sha512", alg)
}

func encode(b []byte, enc string) (string, error) {
	switch enc {
	case "", "hex":
		return hex.EncodeToString(b), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(b), nil
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(b), nil
	}
	return "", fmt.Errorf("encoding %q: one of hex, base64, base64url", enc)
}

func parseKey(pemText string) (any, error) {
	blk, _ := pem.Decode([]byte(pemText))
	if blk == nil {
		return nil, errors.New("key: not PEM")
	}
	switch blk.Type {
	case "PRIVATE KEY":
		return x509.ParsePKCS8PrivateKey(blk.Bytes)
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(blk.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(blk.Bytes)
	case "PUBLIC KEY":
		return x509.ParsePKIXPublicKey(blk.Bytes)
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(blk.Bytes)
	case "CERTIFICATE":
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return nil, err
		}
		return c.PublicKey, nil
	}
	return nil, fmt.Errorf("key: PEM type %q", blk.Type)
}

func digest(h crypto.Hash, data []byte) []byte {
	d := h.New()
	d.Write(data)
	return d.Sum(nil)
}

// sign signs with RS256, RS512, ES256 (raw r||s, as JWS wants) or Ed25519.
func sign(alg, pemText string, data []byte) ([]byte, error) {
	key, err := parseKey(pemText)
	if err != nil {
		return nil, err
	}
	switch alg {
	case "RS256", "RS512":
		k, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s needs an RSA private key", alg)
		}
		h := crypto.SHA256
		if alg == "RS512" {
			h = crypto.SHA512
		}
		return rsa.SignPKCS1v15(rand.Reader, k, h, digest(h, data))
	case "ES256":
		k, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("ES256 needs an EC private key")
		}
		r, s, err := ecdsa.Sign(rand.Reader, k, digest(crypto.SHA256, data))
		if err != nil {
			return nil, err
		}
		out := make([]byte, 64)
		r.FillBytes(out[:32])
		s.FillBytes(out[32:])
		return out, nil
	case "Ed25519", "EdDSA":
		k, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("Ed25519 needs an Ed25519 private key")
		}
		return ed25519.Sign(k, data), nil
	}
	return nil, fmt.Errorf("sign: %q is one of RS256, RS512, ES256, Ed25519", alg)
}

func verify(alg, pemText string, data, sig []byte) (bool, error) {
	key, err := parseKey(pemText)
	if err != nil {
		return false, err
	}
	switch k := key.(type) {
	case *rsa.PrivateKey:
		key = &k.PublicKey
	case *ecdsa.PrivateKey:
		key = &k.PublicKey
	case ed25519.PrivateKey:
		key = k.Public()
	}
	switch alg {
	case "RS256", "RS512":
		k, ok := key.(*rsa.PublicKey)
		if !ok {
			return false, fmt.Errorf("%s needs an RSA key", alg)
		}
		h := crypto.SHA256
		if alg == "RS512" {
			h = crypto.SHA512
		}
		return rsa.VerifyPKCS1v15(k, h, digest(h, data), sig) == nil, nil
	case "ES256":
		k, ok := key.(*ecdsa.PublicKey)
		if !ok || len(sig) != 64 {
			return false, errors.New("ES256 needs an EC key and a 64-byte signature")
		}
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		return ecdsa.Verify(k, digest(crypto.SHA256, data), r, s), nil
	case "Ed25519", "EdDSA":
		k, ok := key.(ed25519.PublicKey)
		if !ok {
			return false, errors.New("Ed25519 needs an Ed25519 key")
		}
		return ed25519.Verify(k, data, sig), nil
	}
	return false, fmt.Errorf("verify: %q is one of RS256, RS512, ES256, Ed25519", alg)
}

func jwt(alg, pemText string, claims, header map[string]any) (string, error) {
	h := map[string]any{"alg": alg, "typ": "JWT"}
	if alg == "Ed25519" {
		h["alg"] = "EdDSA"
	}
	for k, v := range header {
		if k != "alg" {
			h[k] = v
		}
	}
	hb, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sig, err := sign(alg, pemText, []byte(input))
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Crypto runs one crypto.* operation outside a plugin — the author tools give
// test.js the same functions, to sign the callbacks a test feeds a plugin.
func Crypto(op string, arg []byte) ([]byte, error) {
	res, err := cryptoOp(op, arg)
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}
