package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func parseCert(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("no CERTIFICATE block in %q", certPEM)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func writeTemp(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// certWithWindow builds a self-signed cert whose validity window the test picks,
// for the cases GenerateSelfSigned cannot produce (not yet valid, expired).
func certWithWindow(t *testing.T, notBefore, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "window"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// The fallback cert for a bare-IP install: a client that pins it must be able to
// verify the IP as a SAN (not a CN — modern clients ignore the CN), and the key
// must be the one that signed it.
func TestGenerateSelfSignedForIP(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSigned("203.0.113.5")
	if err != nil {
		t.Fatal(err)
	}
	cert := parseCert(t, certPEM)
	if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(net.ParseIP("203.0.113.5")) {
		t.Errorf("IP SANs = %v, want [203.0.113.5]", cert.IPAddresses)
	}
	if len(cert.DNSNames) != 0 {
		t.Errorf("an IP host produced DNS SANs %v", cert.DNSNames)
	}
	if err := cert.VerifyHostname("203.0.113.5"); err != nil {
		t.Errorf("the cert does not verify for its own IP: %v", err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Errorf("cert and key do not pair: %v", err)
	}
	// Signed by its own key (CheckSignatureFrom would demand a CA parent, and a
	// leaf that is not a CA is exactly what a server cert should be).
	if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		t.Errorf("not self-signed: %v", err)
	}
	if cert.IsCA {
		t.Error("the fallback leaf is marked as a CA")
	}
	now := time.Now()
	if !cert.NotBefore.Before(now) {
		t.Error("NotBefore is in the future; a box whose clock is slightly ahead of the client would fail")
	}
	if days := cert.NotAfter.Sub(now).Hours() / 24; days < 360 || days > 367 {
		t.Errorf("validity is %.0f days, want about a year", days)
	}
	hasServerAuth := false
	for _, u := range cert.ExtKeyUsage {
		hasServerAuth = hasServerAuth || u == x509.ExtKeyUsageServerAuth
	}
	if !hasServerAuth {
		t.Error("ExtKeyUsage lacks ServerAuth; browsers reject the cert for TLS servers")
	}
}

func TestGenerateSelfSignedForDomain(t *testing.T) {
	certPEM, _, err := GenerateSelfSigned("panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	cert := parseCert(t, certPEM)
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "panel.example.com" {
		t.Errorf("DNS SANs = %v", cert.DNSNames)
	}
	if len(cert.IPAddresses) != 0 {
		t.Errorf("a DNS host produced IP SANs %v", cert.IPAddresses)
	}
	if cert.Subject.CommonName != "panel.example.com" {
		t.Errorf("CN = %q", cert.Subject.CommonName)
	}
	// Each call is a distinct certificate (random serial + key): two panels must
	// never share a pin.
	other, _, _ := GenerateSelfSigned("panel.example.com")
	if parseCert(t, other).SerialNumber.Cmp(cert.SerialNumber) == 0 {
		t.Error("two generated certs share a serial number")
	}
}

// The pin is what a client config carries; Xray wants lowercase hex of the DER
// leaf, the same digits `openssl x509 -fingerprint -sha256` prints.
func TestCertPinSHA256(t *testing.T) {
	certPEM, _, err := GenerateSelfSigned("203.0.113.5")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	sum := sha256.Sum256(block.Bytes)
	want := hex.EncodeToString(sum[:])

	path := writeTemp(t, "cert.pem", certPEM)
	got, err := CertPinSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("pin = %s, want %s", got, want)
	}
	if len(got) != 64 || got != strings.ToLower(got) {
		t.Errorf("pin %q is not 64 lowercase hex digits", got)
	}
	if _, err := CertPinSHA256(filepath.Join(t.TempDir(), "absent.pem")); err == nil {
		t.Error("a missing file produced a pin")
	}
	if _, err := CertPinSHA256(writeTemp(t, "junk.pem", []byte("not pem"))); err == nil {
		t.Error("a non-PEM file produced a pin")
	}
}

// CertCovers answers the question the domain-change path asks: not "is there a
// fresh cert" but "does the cert on disk prove THIS name".
func TestCertCoversChecksTheHostNotFreshness(t *testing.T) {
	certPEM, _, err := GenerateSelfSigned("old.example.com")
	if err != nil {
		t.Fatal(err)
	}
	path := writeTemp(t, "cert.pem", certPEM)
	if !CertCovers(path, "old.example.com") {
		t.Error("the cert does not cover its own name")
	}
	if CertCovers(path, "new.example.com") {
		t.Error("a fresh cert for the old name was accepted for the new one")
	}
	if CertCovers(path, "") {
		t.Error("an empty host was covered")
	}
	if CertCovers(filepath.Join(t.TempDir(), "absent.pem"), "old.example.com") {
		t.Error("a missing file covered a host")
	}
	ipPEM, _, _ := GenerateSelfSigned("203.0.113.5")
	if !CertCovers(writeTemp(t, "ip.pem", ipPEM), "203.0.113.5") {
		t.Error("an IP cert does not cover its IP")
	}
}

func TestReadCertInfoAndUsable(t *testing.T) {
	certPEM, _, err := GenerateSelfSigned("panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	path := writeTemp(t, "cert.pem", certPEM)
	info, err := ReadCertInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Subject != "panel.example.com" || info.Issuer != "panel.example.com" {
		t.Errorf("subject/issuer = %q/%q, want the host for a self-signed cert", info.Subject, info.Issuer)
	}
	if info.DaysLeft < 360 || info.DaysLeft > 366 {
		t.Errorf("DaysLeft = %d, want about a year", info.DaysLeft)
	}
	if !Usable(path) {
		t.Error("a freshly generated cert is not usable")
	}

	// The window is checked from both ends: a cert that is not valid YET fails a
	// client exactly like an expired one, and a box with a slow clock can hold one.
	now := time.Now()
	future := writeTemp(t, "future.pem", certWithWindow(t, now.Add(time.Hour), now.Add(48*time.Hour)))
	if Usable(future) {
		t.Error("a not-yet-valid cert was reported usable")
	}
	expired := writeTemp(t, "expired.pem", certWithWindow(t, now.Add(-48*time.Hour), now.Add(-time.Hour)))
	if Usable(expired) {
		t.Error("an expired cert was reported usable")
	}
	if Usable(filepath.Join(t.TempDir(), "absent.pem")) || Usable(writeTemp(t, "junk.pem", []byte("junk"))) {
		t.Error("a missing or unparseable cert was reported usable")
	}
	if _, err := ReadCertInfo(writeTemp(t, "junk2.pem", []byte("junk"))); err == nil {
		t.Error("ReadCertInfo accepted a non-PEM file")
	}
}

// A subject-less cert (some ACME issuers leave the CN empty) still gets a
// display name from its SANs, so the certificate card never shows a blank.
func TestReadCertInfoFallsBackToSANsForTheSubject(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		DNSNames:     []string{"san.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := writeTemp(t, "san.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	info, err := ReadCertInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Subject != "san.example.com" {
		t.Errorf("Subject = %q, want the first DNS SAN", info.Subject)
	}
}

// The pair lands with the key private, no staging files left behind, and a
// second write replaces both halves — never a new cert beside an old key.
func TestWriteKeyPair(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSigned("a.example.com")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "certs", "nested")
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := WriteKeyPair(certPath, keyPath, certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{certPath + ".new", keyPath + ".new"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("staging file %s was left behind", p)
		}
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(keyPath)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("key mode = %o, want 0600", fi.Mode().Perm())
		}
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Errorf("the written pair does not load: %v", err)
	}

	certPEM2, keyPEM2, _ := GenerateSelfSigned("b.example.com")
	if err := WriteKeyPair(certPath, keyPath, certPEM2, keyPEM2); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Errorf("after replacement the pair does not load (cert and key from different writes?): %v", err)
	}
	if !CertCovers(certPath, "b.example.com") {
		t.Error("the replacement cert was not written")
	}
}
