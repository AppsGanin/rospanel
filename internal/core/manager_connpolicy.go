package core

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/ipblock"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The source policy: where a client may connect from (see model.ConnPolicy). Every
// sighting the panel records — its own access log and every node's report — is
// checked against it, so one rule covers the whole fleet and every protocol,
// including the ones Xray does not carry.
//
// Enforcement is per ADDRESS, not per account: the offender's address is dropped at
// the firewall of every server, while the account keeps working from wherever the
// policy does allow. Cutting the account instead would punish a user for one bad
// connection — and would be the wrong answer for the traveller as well as the
// reseller.

// policyReportEvery is how often one address may produce a verdict again. A refused
// client retries constantly, and neither the journal nor the firewall wants a row
// per retry; the block itself lasts hours, so an hour between verdicts is invisible
// to enforcement and keeps the log readable.
const policyReportEvery = time.Hour

// policySeenMax bounds the "already dealt with" map. Past it the map is swept of
// stale entries; anything still recent is kept, so the rate limit holds.
const policySeenMax = 4096

// policyState is the cached policy plus the addresses recently acted on.
type policyState struct {
	mu     sync.Mutex
	loaded bool
	policy model.ConnPolicy
	seen   map[string]time.Time
}

// ConnPolicy returns the policy in force, reading it once and keeping it: the check
// runs on the connection path and must not touch the database per sighting.
func (m *Manager) ConnPolicy() model.ConnPolicy {
	m.policy.mu.Lock()
	defer m.policy.mu.Unlock()
	if !m.policy.loaded {
		set, err := m.store.GetSettings()
		if err != nil {
			return model.DefaultConnPolicy() // unreadable settings refuse nobody
		}
		m.policy.policy = set.ConnPolicy
		m.policy.loaded = true
	}
	return m.policy.policy
}

// SaveConnPolicy validates and stores the policy, then puts it in force. Switching
// enforcement off lifts every block it made — an operator turning the rule off
// expects the people it cut to come back, not to wait out a 24-hour timer.
func (m *Manager) SaveConnPolicy(p model.ConnPolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	p = p.Normalized()
	if err := m.store.SetConnPolicy(p); err != nil {
		return err
	}
	m.policy.mu.Lock()
	m.policy.policy = p
	m.policy.loaded = true
	m.policy.seen = nil // a changed rule deserves a fresh verdict on every address
	m.policy.mu.Unlock()

	if p.Enforce && p.Active() {
		m.policyBlock.Arm()
		m.policyBlock.WithTTL(policyTTL(p))
		// Re-arming after a Clear() leaves the kernel empty; put back what the panel
		// still considers blocked so the two agree.
		if ips, err := m.store.BlockedIPList(); err == nil {
			_ = m.policyBlock.Sync(ips)
		}
	} else {
		_ = m.policyBlock.Clear()
		if err := m.store.ClearBlockedIPs(); err != nil {
			logErr("policy: could not clear the block list", "err", err)
		}
	}
	m.notifyNodes() // the nodes' copy of the list changed
	return nil
}

// policyTTL is how long this policy's blocks last: what the operator set, or the
// blocker's own default when they left it at zero.
func policyTTL(p model.ConnPolicy) time.Duration {
	if p.BlockHours <= 0 {
		return ipblock.DefaultTTL
	}
	return time.Duration(p.BlockHours) * time.Hour
}

// CheckConnPolicy is the connection-path check: one sighting, one verdict. Cheap
// when the policy is off (a mutex and a bool), and bounded when it is on — two
// in-memory lookups and a scan over two short lists, with the acting rate-limited
// per address.
func (m *Manager) CheckConnPolicy(userID int64, ip string) {
	p := m.ConnPolicy()
	if !p.Active() {
		return
	}
	if _, err := netip.ParseAddr(ip); err != nil {
		return
	}
	cc := m.CountryOfIP(ip)
	asn, org := m.ASNOfIP(ip)
	v := p.Decide(cc, asn, org)
	if !v.Refused || !m.policyActOnce(ip) {
		return
	}
	m.applyPolicyVerdict(userID, ip, v, p)
}

// policyActOnce reports whether this address is due a verdict, and marks it as
// dealt with. The map is swept rather than allowed to grow: a refused client
// reconnects from the same address forever, and a public panel sees a long tail of
// them.
func (m *Manager) policyActOnce(ip string) bool {
	now := time.Now()
	m.policy.mu.Lock()
	defer m.policy.mu.Unlock()
	if m.policy.seen == nil {
		m.policy.seen = map[string]time.Time{}
	}
	if last, ok := m.policy.seen[ip]; ok && now.Sub(last) < policyReportEvery {
		return false
	}
	if len(m.policy.seen) >= policySeenMax {
		for k, t := range m.policy.seen {
			if now.Sub(t) >= policyReportEvery {
				delete(m.policy.seen, k)
			}
		}
	}
	m.policy.seen[ip] = now
	return true
}

// applyPolicyVerdict records a refusal and, when the policy enforces, drops the
// address at this server's firewall and hands it to the nodes.
func (m *Manager) applyPolicyVerdict(userID int64, ip string, v model.Verdict, p model.ConnPolicy) {
	logInfo("policy: connection refused", "ip", ip, "user", userID, "why", v.String(), "enforced", p.Enforce)
	if userID > 0 {
		m.audit(context.Background(), userID, model.EventPolicyRefused, map[string]any{
			"reason": v.Reason, "country": v.Country, "asn": v.ASN, "org": v.Org,
			"blocked": p.Enforce,
		})
	}
	if !p.Enforce {
		return
	}
	now := time.Now()
	ttl := policyTTL(p)
	if err := m.store.BlockIP(model.BlockedIP{
		IP: ip, Reason: v.Reason, Country: v.Country, ASN: v.ASN, Org: v.Org,
		UserID: userID, At: now.Unix(), Until: now.Add(ttl).Unix(),
	}); err != nil {
		logErr("policy: could not record the block", "ip", ip, "err", err)
	}
	m.policyBlock.WithTTL(ttl)
	if err := m.policyBlock.BlockIP(ip); err != nil {
		logErr("policy: firewall block failed", "ip", ip, "err", err)
	}
	m.notifyNodes() // so the nodes drop it too
}

// ASNOfIP resolves an address to its network, or (0, "") when the table cannot
// place it or has not been downloaded.
func (m *Manager) ASNOfIP(ip string) (uint32, string) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return 0, ""
	}
	lk := m.asnLookup()
	if lk == nil {
		return 0, ""
	}
	asn, org, ok := lk.Lookup(addr)
	if !ok {
		return 0, ""
	}
	return asn, org
}

// BlockedIPs is the live block list for the panel (most recent first).
func (m *Manager) BlockedIPs(limit int) ([]model.BlockedIP, error) {
	return m.store.ListBlockedIPs(limit)
}

// UnblockIP lifts one block by hand, everywhere. Reports whether there was one.
func (m *Manager) UnblockIP(ip string) (bool, error) {
	gone, err := m.store.UnblockIP(ip)
	if err != nil {
		return false, err
	}
	if err := m.policyBlock.UnblockIP(ip); err != nil {
		logErr("policy: firewall unblock failed", "ip", ip, "err", err)
	}
	// The address also has to come out of what the nodes are holding, and the rate
	// limiter must forget it — an operator lifting a block means "let them in", not
	// "let them in until the next packet".
	m.policy.mu.Lock()
	delete(m.policy.seen, ip)
	m.policy.mu.Unlock()
	if gone {
		m.notifyNodes()
	}
	return gone, nil
}

// PurgePolicyBlocks drops lapsed rows so the table, and what the nodes are handed,
// stay in step with the kernel (whose own elements expire on their own).
func (m *Manager) PurgePolicyBlocks() {
	n, err := m.store.PurgeBlockedIPs(time.Now().Unix())
	if err != nil {
		logErr("policy: purge failed", "err", err)
		return
	}
	if n > 0 {
		logInfo("policy: blocks expired", "count", n)
		m.notifyNodes()
	}
}

// ApplyPolicyBlocksAtBoot puts the recorded blocks back into this machine's
// firewall: the kernel forgets everything on restart, and the panel's own record is
// what says who should still be out.
func (m *Manager) ApplyPolicyBlocksAtBoot() {
	p := m.ConnPolicy()
	if !p.Enforce || !p.Active() {
		_ = m.policyBlock.Clear()
		return
	}
	m.policyBlock.WithTTL(policyTTL(p))
	ips, err := m.store.BlockedIPList()
	if err != nil {
		logErr("policy: could not read the block list", "err", err)
		return
	}
	if err := m.policyBlock.Sync(ips); err != nil {
		logErr("policy: could not install the block list", "err", err)
	}
}

// CanBlockIPs reports whether this machine can drop an address at all (Linux with
// nftables). Without it the policy still records every refusal — and the panel says
// so, rather than letting an operator believe a rule is being enforced.
func (m *Manager) CanBlockIPs() bool { return ipblock.Available() }
