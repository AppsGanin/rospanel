package core

import (
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	bruteWindow   = 60 * time.Second // sliding window for counting attempts
	bruteMaxTries = 5                // attempts within window trigger a ban
	bruteBanTime  = time.Hour        // how long the ban lasts
)

// bruteGuard counts failed SOCKS/HTTP-proxy auth attempts per source IP and
// bans repeat offenders via iptables for bruteBanTime.
type bruteGuard struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	banned   map[string]time.Time // ip → expiry
}

func newBruteGuard() *bruteGuard {
	g := &bruteGuard{
		attempts: make(map[string][]time.Time),
		banned:   make(map[string]time.Time),
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

func (g *bruteGuard) ban(ip string) {
	tool := iptoolFor(ip)
	if tool == "" {
		return
	}
	if err := exec.Command(tool, "-I", "INPUT", "1", "-s", ip, "-j", "DROP").Run(); err != nil {
		logErr("brute-guard: ban failed", "tool", tool, "ip", ip, "err", err)
	} else {
		logWarn("brute-guard: banned", "ip", ip, "duration", bruteBanTime)
	}
}

func (g *bruteGuard) unban(ip string) {
	tool := iptoolFor(ip)
	if tool == "" {
		return
	}
	if err := exec.Command(tool, "-D", "INPUT", "-s", ip, "-j", "DROP").Run(); err != nil {
		logErr("brute-guard: unban failed", "tool", tool, "ip", ip, "err", err)
	} else {
		logInfo("brute-guard: unbanned", "ip", ip)
	}
}

// cleanupLoop checks every minute for expired bans and removes them.
func (g *bruteGuard) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		var expired []string
		g.mu.Lock()
		for ip, exp := range g.banned {
			if now.After(exp) {
				delete(g.banned, ip)
				expired = append(expired, ip)
			}
		}
		g.mu.Unlock()
		for _, ip := range expired {
			g.unban(ip)
		}
	}
}

// iptoolFor returns "iptables" for IPv4, "ip6tables" for IPv6, or "" for
// unparseable addresses (so we never shell out with untrusted input).
func iptoolFor(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	if parsed.To4() != nil {
		return "iptables"
	}
	return "ip6tables"
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
