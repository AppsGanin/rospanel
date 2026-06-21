package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// CertPinSHA256 returns the hex-encoded SHA-256 of the leaf certificate's DER
// bytes at path. Clients pin this via pinnedPeerCertSha256 to trust a specific
// (e.g. self-signed) cert without CA verification — the modern replacement for
// allowInsecure, and stricter (only this exact cert is accepted). Xray expects
// lowercase hex (NOT base64), matching `openssl x509 -fingerprint -sha256`.
func CertPinSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", fmt.Errorf("no PEM block in %s", path)
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// GenerateSelfSigned creates a self-signed certificate for host (an IP address or
// a DNS name), valid ~1 year, returning PEM cert+key. It's a fallback so the panel
// can still serve TLS when ACME is unavailable (e.g. rate-limited); the renew loop
// replaces it with a real cert once ACME succeeds.
func GenerateSelfSigned(host string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else if host != "" {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// CertInfo is summary metadata about a certificate on disk.
type CertInfo struct {
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	DaysLeft  int       `json:"days_left"`
}

// ReadCertInfo parses the leaf certificate at path for display.
func ReadCertInfo(path string) (*CertInfo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	subject := cert.Subject.CommonName
	if subject == "" && len(cert.DNSNames) > 0 {
		subject = cert.DNSNames[0]
	}
	if subject == "" && len(cert.IPAddresses) > 0 {
		subject = cert.IPAddresses[0].String()
	}
	return &CertInfo{
		Subject:   subject,
		Issuer:    cert.Issuer.CommonName,
		NotBefore: cert.NotBefore,
		NotAfter:  cert.NotAfter,
		DaysLeft:  int(time.Until(cert.NotAfter).Hours() / 24),
	}, nil
}

// WriteKeyPair writes cert and key PEM to the given paths (0600 key). Both files
// are staged to <path>.new first, then renamed back-to-back, so the window in
// which a concurrent reader (e.g. an Xray reload) could see a new cert beside an
// old key shrinks to the gap between two rename syscalls instead of spanning a
// whole file write. (Truly atomic cross-file replacement isn't possible.)
func WriteKeyPair(certPath, keyPath string, certPEM, keyPEM []byte) error {
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	certTmp, keyTmp := certPath+".new", keyPath+".new"
	if err := os.WriteFile(certTmp, certPEM, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(keyTmp, keyPEM, 0o600); err != nil {
		_ = os.Remove(certTmp)
		return err
	}
	if err := os.Rename(certTmp, certPath); err != nil {
		_ = os.Remove(certTmp)
		_ = os.Remove(keyTmp)
		return err
	}
	if err := os.Rename(keyTmp, keyPath); err != nil {
		_ = os.Remove(keyTmp)
		return err
	}
	return nil
}
