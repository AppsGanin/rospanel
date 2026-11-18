package tlsmgr

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
)

// offlineACME returns an acmeDir that makes ObtainACME fail before any network call:
// the account key directory would have to live under a regular file.
func offlineACME(t *testing.T) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(f, "acme")
}

func writeCert(t *testing.T, host string) (certPath, keyPath string, certPEM []byte) {
	t.Helper()
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	cert, key, err := tlsutil.GenerateSelfSigned(host)
	if err != nil {
		t.Fatal(err)
	}
	if err := tlsutil.WriteKeyPair(certPath, keyPath, cert, key); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath, cert
}

// After a host change with ACME down, the old cert still reads as usable but names the
// old host. Keeping it fails every client's name check; the fallback must replace it.
func TestEnsureFallbackReplacesCertForOldHost(t *testing.T) {
	certPath, keyPath, _ := writeCert(t, "old.example.com")
	set := &model.Settings{Host: "new.example.com"}
	if err := Ensure(set, certPath, keyPath, offlineACME(t), false); err == nil {
		t.Fatal("Ensure succeeded with ACME unreachable")
	}
	if !tlsutil.CertCovers(certPath, "new.example.com") {
		t.Fatal("fallback kept the certificate for the old host")
	}
}

// A forced issue is the panel's host change: on failure SetACMETarget restores the
// old host, so the cert for that host must still be on disk.
func TestEnsureForcedFailureKeepsCertForOldHost(t *testing.T) {
	certPath, keyPath, before := writeCert(t, "old.example.com")
	set := &model.Settings{Host: "new.example.com"}
	if err := Ensure(set, certPath, keyPath, offlineACME(t), true); err == nil {
		t.Fatal("Ensure succeeded with ACME unreachable")
	}
	after, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a failed forced issue replaced the certificate the rollback relies on")
	}
}

func TestEnsureFallbackKeepsCertForSameHost(t *testing.T) {
	certPath, keyPath, before := writeCert(t, "panel.example.com")
	set := &model.Settings{Host: "panel.example.com"}
	_ = Ensure(set, certPath, keyPath, offlineACME(t), false)
	after, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("fallback replaced a usable certificate for the configured host")
	}
}
