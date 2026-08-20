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
	target = NormalizeACMEHost(target)
	email = strings.TrimSpace(email)
	if target == "" {
		return invalidCode("err.hostRequired", "укажите домен или IP-адрес")
	}
	if provider != model.ACMEProviderZeroSSL {
		provider = model.ACMEProviderLE // default
	}
	if !validACMETarget(target, provider) {
		if provider == model.ACMEProviderZeroSSL {
			return invalidCode("err.zerosslDomainsOnly", "ZeroSSL поддерживает только домены (не IP): {{value}} — это не похоже на домен", map[string]any{"value": target})
		}
		return invalidCode("err.notDomainOrIP", "{{value}} — это не похоже на домен или IP-адрес", map[string]any{"value": target})
	}
	if email != "" && !validEmail(email) {
		return invalidCode("err.notEmail", "{{value}} — это не похоже на e-mail адрес", map[string]any{"value": email})
	}
	if provider == model.ACMEProviderZeroSSL && email == "" {
		return invalidCode("err.zerosslNeedsEmail", "ZeroSSL требует e-mail адрес")
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
				return fmt.Errorf("fetching the ZeroSSL EAB: %w", err)
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
	if err := m.ensureCert(set, true); err != nil {
		logErr("tls: certificate issuance failed", "target", target, "err", err)
		// Put the host and SNI back. They were persisted before the attempt, and the
		// generated config takes its ServerName from them — so leaving them pointing at
		// a name the cert on disk cannot prove means the next reconcile (any user add)
		// tells Xray to demand an SNI it cannot serve, and rejectUnknownSni closes :443
		// with the panel behind it. The operator's change simply did not happen.
		if rerr := m.store.SetTLSMode(cur.TLSMode, cur.Host, cur.SNI, cur.ACMEEmail); rerr != nil {
			logErr("tls: could not restore the previous target after a failed issue", "err", rerr)
		}
		return err
	}
	logInfo("tls: certificate issued", "target", target)
	// A restart, not just a reconcile. The cert PATH is what lives in the config, so a
	// re-issue regenerates a byte-identical file and Supervisor.Apply short-circuits
	// before restarting — Xray would keep presenting the previous certificate for up to
	// an hour while the panel reports success. This is the same trap tlsLoop and the
	// node's certLoop already guard against.
	m.TriggerReconcile()
	if err := m.RestartXray(); err != nil {
		logErr("tls: restarting Xray after issuing a certificate failed", "err", err)
	}
	return nil
}

// ensureCert serializes certificate writes. tlsLoop retries every few minutes while no CA
// cert exists, and the operator can trigger an issue from the panel at the same moment;
// two concurrent writers share fixed staging filenames and rename cert and key
// separately, so an overlap can pair one issuance's certificate with another's key —
// which Xray cannot serve and no health check notices. The node path already had this
// lock; the master did not.
func (m *Manager) ensureCert(set *model.Settings, force bool) error {
	m.certMu.Lock()
	defer m.certMu.Unlock()
	return tlsmgr.Ensure(set, m.tls.CertPath, m.tls.KeyPath, m.tls.ACMEDir, force)
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
	if err := m.ensureCert(set, false); err != nil {
		m.notifyCertError(set.Host, err)
		return false, err
	}
	after, _ := tlsutil.ReadCertInfo(m.tls.CertPath)
	changed := before == nil || after == nil || !before.NotAfter.Equal(after.NotAfter)
	if changed {
		logInfo("tls: certificate renewed", "host", set.Host)
		daysLeft := 0
		if after != nil {
			daysLeft = after.DaysLeft
		}
		m.notifyCertRenewed(set.Host, daysLeft)
	}
	return changed, nil
}
