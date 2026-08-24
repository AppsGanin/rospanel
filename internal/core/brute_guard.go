package core

import (
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	bruteWindow    = 60 * time.Second // sliding window for counting attempts
	bruteMaxTries  = 5                // attempts within window trigger a ban
	bruteBanTime   = time.Hour        // how long the ban lasts
	bruteBanTTL    = "1h"             // kernel timeout string
	bruteTableName = "rospanel_bruteguard"
)

const bruteRuleset = `add table inet rospanel_bruteguard
add set inet rospanel_bruteguard banned4 { type ipv4_addr; flags timeout; }
add set inet rospanel_bruteguard banned6 { type ipv6_addr; flags timeout; }
add chain inet rospanel_bruteguard input { type filter hook input priority -6; policy accept; }
add rule inet rospanel_bruteguard input iif "lo" accept
add rule inet rospanel_bruteguard input ip saddr @banned4 drop
add rule inet rospanel_bruteguard input ip6 saddr @banned6 drop
`

// bruteGuard counts failed SOCKS/HTTP-proxy auth attempts per source IP and
// bans repeat offenders via nftables for bruteBanTime.
type bruteGuard struct {
	mu             sync.Mutex
	attempts       map[string][]time.Time
	banned         map[string]time.Time // ip → expiry
	nftMu          sync.Mutex
	ensureFailedAt time.Time
}

func newBruteGuard() *bruteGuard {
	g := &bruteGuard{
		attempts: make(map[string][]time.Time),
		banned:   make(map[string]time.Time),
	}
	go g.cleanupLoop()
	return g
}

func bruteNFTAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("nft")
	return err == nil
}

func (g *bruteGuard) ensureNFT() error {
	if out, err := exec.Command("nft", "list", "table", "inet", bruteTableName).CombinedOutput(); err == nil {
		if strings.Contains(string(out), "flags timeout") {
			return nil
		}
		_ = exec.Command("nft", "delete", "table", "inet", bruteTableName).Run()
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(bruteRuleset)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft install bruteguard table: %w\n%s", err, out)
	}
	logInfo("bruteguard: nftables drop table installed", "table", bruteTableName)
	return nil
}

func bruteSetFor(ip string) (string, netip.Addr, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", netip.Addr{}, false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return "banned4", addr, true
	}
	return "banned6", addr, true
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
	if !bruteNFTAvailable() {
		logWarn("brute-guard: banned (memory-only, nft not available)", "ip", ip, "duration", bruteBanTime)
		return
	}
	set, addr, ok := bruteSetFor(ip)
	if !ok {
		return
	}
	g.nftMu.Lock()
	defer g.nftMu.Unlock()
	if !g.ensureFailedAt.IsZero() && time.Since(g.ensureFailedAt) < 5*time.Minute {
		return
	}
	if err := g.ensureNFT(); err != nil {
		g.ensureFailedAt = time.Now()
		logErr("brute-guard: nft ensure failed", "err", err)
		return
	}
	elem := fmt.Sprintf("{ %s timeout %s }", addr.String(), bruteBanTTL)
	out, err := exec.Command("nft", "add", "element", "inet", bruteTableName, set, elem).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		g.ensureFailedAt = time.Now()
		logErr("brute-guard: ban failed", "ip", ip, "err", err, "out", string(out))
		return
	}
	g.ensureFailedAt = time.Time{}
	logWarn("brute-guard: banned", "ip", ip, "duration", bruteBanTime)
}

func (g *bruteGuard) unban(ip string) {
	if !bruteNFTAvailable() {
		logInfo("brute-guard: unbanned", "ip", ip)
		return
	}
	set, addr, ok := bruteSetFor(ip)
	if !ok {
		return
	}
	g.nftMu.Lock()
	defer g.nftMu.Unlock()
	elem := fmt.Sprintf("{ %s }", addr.String())
	out, err := exec.Command("nft", "delete", "element", "inet", bruteTableName, set, elem).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such file") {
		logErr("brute-guard: unban failed", "ip", ip, "err", err, "out", string(out))
		return
	}
	logInfo("brute-guard: unbanned", "ip", ip)
}

// cleanupLoop checks every minute for expired bans and removes them.
func (g *bruteGuard) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		cutoff := now.Add(-bruteWindow)
		var expired []string
		g.mu.Lock()
		for ip, exp := range g.banned {
			if now.After(exp) {
				delete(g.banned, ip)
				expired = append(expired, ip)
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
		for _, ip := range expired {
			g.unban(ip)
		}
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
