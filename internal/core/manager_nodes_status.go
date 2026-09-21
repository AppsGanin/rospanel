package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/tlsmgr"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
)

// NodeTLSStatus reports a node's effective TLS/ACME status for its Domain tab: its
// address, its own (or inherited) ACME provider/email, and its cert metadata (built
// from what the node last reported). Mirrors the master's TLSStatus.
func (m *Manager) NodeTLSStatus(id int64) (*TLSStatus, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	n, err := m.store.GetNode(id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, invalidCode("err.nodeNotFound", "нода не найдена")
	}
	provider := n.ACMEProvider
	if provider == "" {
		provider = set.ACMEProvider
	}
	if provider == "" {
		provider = model.ACMEProviderLE
	}
	email := n.ACMEEmail
	if email == "" {
		email = set.ACMEEmail
	}
	var cert *tlsutil.CertInfo
	if n.CertExpiresAt > 0 || n.CertIssuer != "" {
		exp := time.Unix(n.CertExpiresAt, 0)
		cert = &tlsutil.CertInfo{
			Issuer:   n.CertIssuer, // "" when self-signed → the panel shows it as temporary
			NotAfter: exp,
			DaysLeft: int(time.Until(exp).Hours() / 24),
		}
	}
	return &TLSStatus{
		Mode:         model.TLSModeACME,
		Domain:       n.Host,
		SNI:          n.Host,
		ACMEEmail:    email,
		ACMEProvider: provider,
		Cert:         cert,
	}, nil
}

// SetNodeACME sets a node's own domain (ACME target), e-mail and CA provider, then
// wakes the node so its agent re-issues the cert. The panel can't issue a remote
// node's cert — the node does that — so this only persists the config and (for
// ZeroSSL) fetches the EAB the node's agent needs.
func (m *Manager) SetNodeACME(id int64, target, email, provider string) error {
	n, err := m.store.GetNode(id)
	if err != nil {
		return err
	}
	if n == nil {
		return invalidCode("err.nodeNotFound", "нода не найдена")
	}
	target = NormalizeACMEHost(target)
	email = strings.TrimSpace(email)
	if target == "" {
		return invalidCode("err.hostRequired", "укажите домен или IP-адрес")
	}
	if provider != model.ACMEProviderZeroSSL {
		provider = model.ACMEProviderLE
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
	// ZeroSSL: reuse the node's stored EAB, else fetch a fresh one for its e-mail.
	eabKID, eabHMAC := "", ""
	if provider == model.ACMEProviderZeroSSL {
		if n.ZeroSSLEABKID != "" {
			eabKID, eabHMAC = n.ZeroSSLEABKID, n.ZeroSSLEABHMAC
		} else {
			kid, hmac, err := tlsmgr.FetchZeroSSLEAB(email)
			if err != nil {
				return fmt.Errorf("fetching the ZeroSSL EAB: %w", err)
			}
			eabKID, eabHMAC = kid, hmac
		}
	}
	if err := m.store.SetNodeACME(id, target, email, provider, eabKID, eabHMAC); err != nil {
		return err
	}
	m.nodes.wakeOne(id)
	return nil
}

// NodeGeoFiles returns a node's last-reported geo database status (nil if it hasn't
// reported yet).
func (m *Manager) NodeGeoFiles(id int64) []nodeapi.GeoFile {
	m.nodeGeoMu.Lock()
	defer m.nodeGeoMu.Unlock()
	return m.nodeGeoFiles[id]
}

// NodeHostStats returns a node's last-reported machine state (ok=false when the
// node hasn't reported one — an agent older than this feature never will).
func (m *Manager) NodeHostStats(id int64) (nodeapi.HostStats, bool) {
	m.nodeGeoMu.Lock()
	defer m.nodeGeoMu.Unlock()
	h, ok := m.nodeHostStats[id]
	return h, ok
}

// nodeAWGState is what a node last said about its AmneziaWG tunnel. Reported tells
// "the node has never mentioned AWG" (an older agent, or one where the lane was never
// switched on) apart from "the node says it is down", which are very different facts.
type nodeAWGState struct {
	Running  bool
	Err      string
	Reported bool
}

// NodeAWG returns a node's last-reported tunnel state (ok=false when it has never
// reported one).
func (m *Manager) NodeAWG(id int64) (nodeAWGState, bool) {
	m.nodeGeoMu.Lock()
	defer m.nodeGeoMu.Unlock()
	st, ok := m.nodeAWG[id]
	return st, ok && st.Reported
}

// NodeSyncFails returns a node's last-reported sync-failure count for the past hour
// (0 if it hasn't reported one).
func (m *Manager) NodeSyncFails(id int64) int {
	m.nodeGeoMu.Lock()
	defer m.nodeGeoMu.Unlock()
	return m.nodeSyncFails[id]
}
