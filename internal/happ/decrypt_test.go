package happ

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"testing"
)

func TestIsHappLink(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"happ://crypt/abc", true},
		{"happ://crypt2/abc", true},
		{"happ://crypt3/abc", true},
		{"happ://crypt4/abc", true},
		{"happ://crypt5/abc", true},
		{"vless://abc", false},
		{"https://example.com/sub", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsHappLink(tc.in); got != tc.want {
			t.Errorf("IsHappLink(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCrypt1to4RSAKeys(t *testing.T) {
	// Verify that embedded RSA keys can encrypt and decrypt payloads with PKCS1v15.
	for i := 0; i < 4; i++ {
		key, err := getRSAKey(i)
		if err != nil {
			t.Fatalf("getRSAKey(%d) failed: %v", i, err)
		}
		secretText := []byte("vless://11111111-2222-3333-4444-555555555555@example.com:443#KeyTest")
		cipher, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, secretText)
		if err != nil {
			t.Fatalf("rsa.EncryptPKCS1v15 failed for key %d: %v", i, err)
		}
		b64Payload := base64.StdEncoding.EncodeToString(cipher)

		prefix := "happ://crypt/"
		if i == 1 {
			prefix = "happ://crypt2/"
		} else if i == 2 {
			prefix = "happ://crypt3/"
		} else if i == 3 {
			prefix = "happ://crypt4/"
		}

		decrypted, err := Decrypt(prefix + b64Payload)
		if err != nil {
			t.Fatalf("Decrypt(%s...) failed: %v", prefix, err)
		}
		if string(decrypted) != string(secretText) {
			t.Errorf("key %d decrypted mismatch: got %q, want %q", i, string(decrypted), string(secretText))
		}
	}
}
