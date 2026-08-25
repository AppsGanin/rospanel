package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"path/filepath"
	"testing"
)

func TestGenerateSelfSignedDomain(t *testing.T) {
	host := "vpn.example.com"
	certPEM, keyPEM, err := GenerateSelfSigned(host)
	if err != nil {
		t.Fatalf("GenerateSelfSigned(%q) error: %v", host, err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("invalid cert PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate error: %v", err)
	}

	if cert.Subject.CommonName != host {
		t.Errorf("cert CommonName = %q; want %q", cert.Subject.CommonName, host)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != host {
		t.Errorf("cert DNSNames = %v; want [%q]", cert.DNSNames, host)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		t.Fatalf("invalid key PEM block")
	}
}

func TestGenerateSelfSignedIP(t *testing.T) {
	ipStr := "198.51.100.1"
	certPEM, keyPEM, err := GenerateSelfSigned(ipStr)
	if err != nil {
		t.Fatalf("GenerateSelfSigned(%q) error: %v", ipStr, err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatalf("invalid cert PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate error: %v", err)
	}

	if len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != ipStr {
		t.Errorf("cert IPAddresses = %v; want [%s]", cert.IPAddresses, ipStr)
	}

	if len(keyPEM) == 0 {
		t.Error("keyPEM is empty")
	}
}

func TestCertLifecycle(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	host := "panel.example.org"
	certPEM, keyPEM, err := GenerateSelfSigned(host)
	if err != nil {
		t.Fatalf("GenerateSelfSigned failed: %v", err)
	}

	if err := WriteKeyPair(certPath, keyPath, certPEM, keyPEM); err != nil {
		t.Fatalf("WriteKeyPair failed: %v", err)
	}

	// Verify CertPinSHA256
	pin, err := CertPinSHA256(certPath)
	if err != nil {
		t.Fatalf("CertPinSHA256 failed: %v", err)
	}
	if len(pin) != 64 {
		t.Errorf("CertPinSHA256 = %q (len %d); want 64 hex chars", pin, len(pin))
	}

	// Verify ReadCertInfo
	info, err := ReadCertInfo(certPath)
	if err != nil {
		t.Fatalf("ReadCertInfo failed: %v", err)
	}
	if info.Subject != host {
		t.Errorf("info.Subject = %q; want %q", info.Subject, host)
	}
	if info.DaysLeft <= 300 {
		t.Errorf("info.DaysLeft = %d; expected ~365 days", info.DaysLeft)
	}

	// Verify Usable
	if !Usable(certPath) {
		t.Error("Usable(certPath) = false; want true")
	}
	if Usable(filepath.Join(dir, "nonexistent.pem")) {
		t.Error("Usable(nonexistent) = true; want false")
	}

	// Verify CertCovers
	if !CertCovers(certPath, host) {
		t.Errorf("CertCovers(%q) = false; want true", host)
	}
	if CertCovers(certPath, "other.example.org") {
		t.Error("CertCovers(other.example.org) = true; want false")
	}
	if CertCovers(certPath, "") {
		t.Error("CertCovers(\"\") = true; want false")
	}
	if CertCovers(filepath.Join(dir, "nonexistent.pem"), host) {
		t.Error("CertCovers(nonexistent) = true; want false")
	}
}
