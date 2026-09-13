package core

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/ipblock"
	"github.com/AppsGanin/rospanel/internal/model"
)

const (
	bruteWindow   = 60 * time.Second // sliding window for counting attempts
	bruteMaxTries = 5                // attempts within window trigger a ban
	bruteBanTime  = time.Hour        // how long the ban lasts
)

// bruteGuard counts failed SOCKS/HTTP-proxy auth attempts per source IP and bans
// repeat offenders for bruteBanTime.
//
// The ban lives in the same kind of nftables set as the panel's other blocks
// (ipblock): one table of its own, elements with a kernel timeout. So the ban
// expires in the kernel whether or not the panel is still running to lift it, a
// restart leaves no rule behind, and nothing here shells out per address to a
// tool the box may not have.
type bruteGuard struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	banned   map[string]time.Time // ip → expiry, so an address is not banned twice
	blocker  *ipblock.Blocker
}

func newBruteGuard() *bruteGuard {
	g := &bruteGuard{
		attempts: make(map[string][]time.Time),
		banned:   make(map[string]time.Time),
		blocker:  ipblock.New(ipblock.TableBrute).WithTTL(bruteBanTime),
	}
	return g
}

// record notes one failed attempt from ip and returns true the first time the
// threshold is crossed (caller should then call ban).
func (g *bruteGuard) record(ip string) bool {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if exp, ok := g.banned[ip]; ok && now.Before(exp) {
		return false // already banned, don't double-ban
	}
	cutoff := now.Add(-bruteWindow)
	prev := g.attempts[ip]
	kept := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	g.attempts[ip] = kept
	if len(kept) >= bruteMaxTries {
		g.banned[ip] = now.Add(bruteBanTime)
		delete(g.attempts, ip)
		return true
	}
	return false
}

// forget drops what the guard remembers about addresses inside nets: a network the
// operator has just trusted, whose ban is being lifted. Left behind, a remembered ban
// would stop the guard from banning the address again should the trust be revoked.
func (g *bruteGuard) forget(nets model.TrustedNets) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for ip := range g.banned {
		if nets.Contains(ip) {
			delete(g.banned, ip)
		}
	}
	for ip := range g.attempts {
		if nets.Contains(ip) {
			delete(g.attempts, ip)
		}
	}
}

// ban drops the address at the firewall for bruteBanTime. The kernel lifts it;
// the guard only has to remember not to ban it again meanwhile.
func (g *bruteGuard) ban(ip string) {
	if net.ParseIP(ip) == nil {
		return // never hand untrusted input to the firewall
	}
	if err := g.blocker.BlockIP(ip); err != nil {
		logErr("brute-guard: ban failed", "ip", ip, "err", err)
		return
	}
	logWarn("brute-guard: banned", "ip", ip, "duration", bruteBanTime)
}

// cleanupLoop forgets expired bans and stale attempt lists once a minute, until done
// closes. The firewall side needs nothing: the elements time out on their own.
//
// It is started by the manager through runAsync rather than from the constructor with
// a bare go, which is what it used to be — and a loop over a ticker with no way out
// outlives the manager that made it. Close could not wait for it, so every manager
// ever built (every test builds several) left one ticking forever.
func (g *bruteGuard) cleanupLoop(done <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}
		now := time.Now()
		cutoff := now.Add(-bruteWindow)
		g.mu.Lock()
		for ip, exp := range g.banned {
			if now.After(exp) {
				delete(g.banned, ip)
			}
		}
		// Sweep stale attempt lists too. An entry is otherwise pruned only when that
		// same IP tries again or crosses the threshold, so a source that stops one try
		// short of a ban leaves its entry behind forever — and the feed is public (the
		// system SOCKS/HTTP inbounds bind 0.0.0.0), where one host with an IPv6 /64 can
		// mint millions of distinct addresses.
		for ip, tries := range g.attempts {
			if len(tries) == 0 || tries[len(tries)-1].Before(cutoff) {
				delete(g.attempts, ip)
			}
		}
		g.mu.Unlock()
	}
}

// bruteGuardLoop subscribes to the Xray log stream and feeds failed-auth lines
// into the brute-force guard.
func (m *Manager) bruteGuardLoop() {
	ch, unsub := m.sup.SubscribeLogs()
	defer unsub()
	for {
		var line string
		select {
		case <-m.done:
			return
		case l, ok := <-ch:
			if !ok {
				return
			}
			line = l
		}
		if ip := parseRejectIP(line); m.noteProxyAuthReject(ip) {
			go m.guard.ban(ip)
		}
	}
}

// noteProxyAuthReject counts one failed proxy sign-in from ip and reports whether it
// has just earned a ban. A trusted address is not counted at all.
func (m *Manager) noteProxyAuthReject(ip string) bool {
	if ip == "" || m.guard == nil || m.Trusted(ip) {
		return false
	}
	return m.guard.record(ip)
}

// parseRejectIP extracts the source IP from an Xray "rejected proxy/socks:"
// log line. Returns "" for any other line or when the address is loopback.
//
//	... from tcp:1.2.3.4:5678 rejected  proxy/socks: invalid username or password
//	... from tcp:1.2.3.4:5678 rejected  proxy/socks: socks 4 is not allowed ...
func parseRejectIP(line string) string {
	if !strings.Contains(line, " rejected ") || !strings.Contains(line, "proxy/socks:") {
		return ""
	}
	f := strings.Index(line, "from tcp:")
	if f < 0 {
		return ""
	}
	rest := line[f+len("from tcp:"):]
	if sp := strings.IndexByte(rest, ' '); sp > 0 {
		rest = rest[:sp]
	}
	host, _, err := net.SplitHostPort(rest)
	if err != nil {
		return ""
	}
	if host == "" || host == "127.0.0.1" || host == "::1" {
		return ""
	}
	return host
}
