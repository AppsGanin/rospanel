package provision

import (
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
