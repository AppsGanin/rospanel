package core

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/ipblock"
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
	go g.cleanupLoop()
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

// cleanupLoop forgets expired bans and stale attempt lists once a minute. The
// firewall side needs nothing: the elements time out on their own.
func (g *bruteGuard) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
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
	for line := range ch {
		ip := parseRejectIP(line)
		if ip == "" {
			continue
		}
		if m.guard.record(ip) {
			go m.guard.ban(ip)
		}
	}
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
