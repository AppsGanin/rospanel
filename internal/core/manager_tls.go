package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/tlsmgr"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
)

// TLSStatus is the current TLS configuration plus active cert metadata.
type TLSStatus struct {
	Mode         string            `json:"mode"`
	Domain       string            `json:"domain"`
	SNI          string            `json:"sni"`
	ACMEEmail    string            `json:"acme_email"`
	ACMEProvider string            `json:"acme_provider"`
	Cert         *tlsutil.CertInfo `json:"cert"`
}

// TLSStatus reports the current TLS settings and active certificate.
func (m *Manager) TLSStatus() (*TLSStatus, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	provider := set.ACMEProvider
	if provider == "" {
		provider = model.ACMEProviderLE
	}
	info, _ := tlsutil.ReadCertInfo(m.tls.CertPath) // nil if unreadable
	return &TLSStatus{
		Mode:         set.TLSMode,
		Domain:       set.Host,
		SNI:          set.SNI,
		ACMEEmail:    set.ACMEEmail,
		ACMEProvider: provider,
		Cert:         info,
	}, nil
}

// SetACMETarget sets the ACME target (a domain OR an IP address), saves the
// chosen CA provider (provider = "letsencrypt" | "zerossl"), obtains a
// certificate, and reloads Xray. host and sni are both set to the target so the
// cert, the client link address and the SNI all match.
func (m *Manager) SetACMETarget(target, email, provider, eabKID, eabHMAC string) error {
	target = strings.TrimSpace(target)
	email = strings.TrimSpace(email)
	if target == "" {
		return invalid("укажите домен или IP-адрес")
	}
	if provider != model.ACMEProviderZeroSSL {
		provider = model.ACMEProviderLE // default
	}
	if !validACMETarget(target, provider) {
		if provider == model.ACMEProviderZeroSSL {
			return invalid("ZeroSSL поддерживает только домены (не IP): %q — это не похоже на домен", target)
		}
		return invalid("%q — это не похоже на домен или IP-адрес", target)
	}
	if email != "" && !validEmail(email) {
		return invalid("%q — это не похоже на e-mail адрес", email)
	}
	if provider == model.ACMEProviderZeroSSL && email == "" {
		return invalid("ZeroSSL требует e-mail адрес")
	}
	cur, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	if email == "" {
		email = cur.ACMEEmail
	}
	if err := m.store.SetTLSMode(model.TLSModeACME, target, target, email); err != nil {
		return err
	}
	// For ZeroSSL: auto-fetch EAB from their public API when not supplied and not
	// already stored. EAB is only needed for the initial account registration —
	// once we have an account key on disk, subsequent renewals skip it.
	if provider == model.ACMEProviderZeroSSL && eabKID == "" {
		cur, _ := m.store.GetSettings()
		if cur.ZeroSSLEABKID != "" {
			eabKID = cur.ZeroSSLEABKID
			eabHMAC = cur.ZeroSSLEABHMAC
		} else {
			kid, hmac, err := tlsmgr.FetchZeroSSLEAB(email)
			if err != nil {
				return fmt.Errorf("получение EAB от ZeroSSL: %w", err)
			}
			eabKID, eabHMAC = kid, hmac
		}
	}
	if err := m.store.SetACMEProvider(provider, eabKID, eabHMAC); err != nil {
		return err
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	// force=true: issue a real cert now for the new target.
	logInfo("tls: issuing certificate", "target", target, "provider", provider)
	if err := tlsmgr.Ensure(set, m.tls.CertPath, m.tls.KeyPath, m.tls.ACMEDir, true); err != nil {
		logErr("tls: certificate issuance failed", "target", target, "err", err)
		return err
	}
	logInfo("tls: certificate issued", "target", target)
	m.TriggerReconcile()
	return nil
}

// HasValidCert reports whether a non-expired, CA-issued certificate is present.
// A self-signed fallback cert is deliberately treated as "not valid" so the renew
// loop stays in its fast-retry cadence until a real ACME cert is obtained.
func (m *Manager) HasValidCert() bool {
	info, err := tlsutil.ReadCertInfo(m.tls.CertPath)
	if err != nil || !time.Now().Before(info.NotAfter) {
		return false
	}
	return info.Issuer != "" && info.Issuer != info.Subject // CA-issued, not self-signed
}

// CertPinSHA256 returns the hex SHA-256 of the active leaf certificate, for
// clients to pin via pinnedPeerCertSha256 when the cert isn't CA-trusted. "" if
// unavailable.
func (m *Manager) CertPinSHA256() string {
	pin, _ := tlsutil.CertPinSHA256(m.tls.CertPath)
	return pin
}

// RenewTLSIfNeeded renews an ACME cert when near expiry. It reports whether the
// certificate actually changed (so the caller can reload Xray only then).
func (m *Manager) RenewTLSIfNeeded() (bool, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return false, err
	}
	if set.TLSMode != model.TLSModeACME {
		return false, nil
	}
	before, _ := tlsutil.ReadCertInfo(m.tls.CertPath)
	if err := tlsmgr.Ensure(set, m.tls.CertPath, m.tls.KeyPath, m.tls.ACMEDir, false); err != nil {
		return false, err
	}
	after, _ := tlsutil.ReadCertInfo(m.tls.CertPath)
	changed := before == nil || after == nil || !before.NotAfter.Equal(after.NotAfter)
	if changed {
		logInfo("tls: certificate renewed", "host", set.Host)
	}
	return changed, nil
}
