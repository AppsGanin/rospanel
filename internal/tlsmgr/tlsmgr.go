// Package tlsmgr obtains the TLS certificate via ACME (Let's Encrypt) for a
// domain or an IP address. ACME is the source of real certificates; when it's
// unavailable (e.g. rate-limited) and no usable cert exists, Ensure writes a
// self-signed fallback so the panel still comes up, and the renew loop swaps in
// the real cert once ACME succeeds.
package tlsmgr

import (
	"fmt"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
)

// Ensure obtains (or renews) the certificate for the configured host into
// certPath/keyPath. force=true always (re)issues; force=false issues only when
// the cert is missing or near expiry. If ACME fails and no usable cert is on
// disk, it writes a self-signed fallback (and returns the ACME error so the
// caller logs it and keeps retrying). An existing usable cert is left untouched.
func Ensure(set *model.Settings, certPath, keyPath, acmeDir string, force bool) error {
	err := ensureACME(set, certPath, keyPath, acmeDir, force)
	if err == nil {
		return nil
	}
	// ACME failed. Keep any usable cert already on disk; otherwise self-sign so
	// Xray/the panel can still serve TLS until ACME succeeds.
	if _, rerr := tlsutil.ReadCertInfo(certPath); rerr != nil {
		cert, key, gerr := tlsutil.GenerateSelfSigned(set.Host)
		if gerr != nil {
			return fmt.Errorf("acme failed (%w); self-signed fallback failed: %v", err, gerr)
		}
		if werr := tlsutil.WriteKeyPair(certPath, keyPath, cert, key); werr != nil {
			return fmt.Errorf("acme failed (%w); writing self-signed failed: %v", err, werr)
		}
		return fmt.Errorf("acme unavailable, serving a self-signed cert for now: %w", err)
	}
	return err
}

// needsRenewal reports whether a CA-issued cert is in the last third of its
// lifetime — so ~6-day IP certs renew ~2 days out while 90-day domain certs
// renew ~30 days out, all from one rule.
func needsRenewal(info *tlsutil.CertInfo) bool {
	lifetime := info.NotAfter.Sub(info.NotBefore)
	if lifetime <= 0 {
		return true
	}
	return time.Until(info.NotAfter) < lifetime/3
}

// ensureACME obtains a cert from the configured ACME CA. Unless force is set,
// it skips when a CA-issued cert is already present and still fresh.
func ensureACME(set *model.Settings, certPath, keyPath, acmeDir string, force bool) error {
	if set.Host == "" {
		return fmt.Errorf("no domain or IP address configured")
	}
	if !force {
		if info, err := tlsutil.ReadCertInfo(certPath); err == nil {
			caIssued := info.Issuer != "" && info.Issuer != info.Subject
			if caIssued && !needsRenewal(info) {
				return nil
			}
		}
	}
	provider := set.ACMEProvider
	if provider == "" {
		provider = model.ACMEProviderLE
	}
	return ObtainACME(set.Host, set.ACMEEmail, certPath, keyPath, acmeDir,
		provider, set.ZeroSSLEABKID, set.ZeroSSLEABHMAC)
}
