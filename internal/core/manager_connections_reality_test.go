package core

import (
	"crypto/x509"
	"testing"
)

// certRecordSize gates the REALITY donor against the #6402 cert-size limit, so its
// arithmetic has to track the real TLS Certificate record closely enough that a
// just-oversized donor (www.microsoft.com, ~8.3 KB) is rejected while a normal one
// passes. Uses synthetic DER lengths — the framing math is what's under test.
func TestCertRecordSize(t *testing.T) {
	cert := func(derLen int) *x509.Certificate {
		return &x509.Certificate{Raw: make([]byte, derLen)}
	}

	// A single ~4 KB leaf + ~1.5 KB intermediate stays comfortably under the limit.
	small := []*x509.Certificate{cert(4000), cert(1500)}
	if got := certRecordSize(small); got >= realityCertLimit {
		t.Errorf("small chain size %d should be under the %d limit", got, realityCertLimit)
	}

	// A microsoft-sized chain (~8.2 KB of DER across two certs) must exceed it.
	big := []*x509.Certificate{cert(5200), cert(3000)}
	if got := certRecordSize(big); got <= realityCertLimit {
		t.Errorf("oversized chain size %d should exceed the %d limit", got, realityCertLimit)
	}
}
