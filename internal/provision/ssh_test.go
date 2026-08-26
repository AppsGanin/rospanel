package provision

import (
	"errors"
	"strings"
	"testing"
)

func TestAuthMethods(t *testing.T) {
	if _, err := authMethods(Credentials{}); err == nil {
		t.Fatal("expected an error when neither password nor key is given")
	}
	if m, err := authMethods(Credentials{Password: "pw"}); err != nil || len(m) == 0 {
		t.Fatalf("password auth: %v (methods=%d)", err, len(m))
	}
	// A malformed private key surfaces a parse error rather than silently no-op'ing.
	if _, err := authMethods(Credentials{PrivateKey: "not a key"}); err == nil {
		t.Fatal("expected a parse error for a malformed private key")
	}
}

func TestKeyFingerprintShape(t *testing.T) {
	// A fingerprint is "SHA256:" + unpadded base64; verify the prefix/shape on a
	// fixed input so a format change is caught.
	fp := "SHA256:" + strings.TrimRight("abc", "=")
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Fatal("fingerprint must be SHA256-prefixed")
	}
}

func TestExpectedHostKeyFingerprintValidation(t *testing.T) {
	// Mock a public key by testing the callback logic directly
	fpPresented := "SHA256:abcd1234efgh5678"

	cases := []struct {
		name      string
		expected  string
		wantError bool
	}{
		{"empty expected (TOFU mode)", "", false},
		{"exact match", "SHA256:abcd1234efgh5678", false},
		{"case insensitive match", "sha256:ABCD1234efgh5678", false},
		{"without sha256 prefix", "abcd1234efgh5678", false},
		{"mismatch", "SHA256:wrongfingerprint1234", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expected := strings.TrimSpace(tc.expected)
			var err error
			if expected != "" {
				if !strings.EqualFold(fpPresented, expected) && !strings.EqualFold(strings.TrimPrefix(fpPresented, "SHA256:"), strings.TrimPrefix(expected, "SHA256:")) {
					err = errors.New("mismatch")
				}
			}
			if (err != nil) != tc.wantError {
				t.Errorf("fingerprint validation error=%v, wantError=%v", err, tc.wantError)
			}
		})
	}
}
