// Package connguard installs host-level nftables rules that cap, per source IP,
// both the number of concurrent TCP connections and the rate of new connections
// to the public proxy ports. This blunts the single-abuser DoS vector that the
// per-user model can't reach: a flood of TLS handshakes (each costs CPU) or a
// connection-exhaustion storm happens at connect time, before any user auth, so
// quotas/device-limits never see it. It is NOT per-user bandwidth shaping — Xray
// has no native per-user speed cap — it protects the box itself.
//
// Like the port-hopping rules, this is best-effort: a no-op when nft is missing
// or off Linux, so the panel never fails to start just because it can't be set
// up here. It lives in its own `inet rospanel_connlimit` table that we own
// wholesale and recreate idempotently.
package connguard

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

const tableName = "rospanel_connlimit"

// Defaults are deliberately generous so normal clients — including several users
// sharing one NAT/CGNAT address — never trip them; they exist to stop egregious
// single-IP floods (thousands of connections / handshakes), not to ration honest
// use. Override per install with the ROSPANEL_CONNLIMIT_* env vars.
const (
	// Tuned for a public service where many unrelated subscribers legitimately share
	// one carrier-grade NAT (CGNAT) address: a single mobile operator IP can carry
	// dozens of active clients, each opening tens of concurrent TCP connections
	// (VLESS-Vision runs without mux, so one app connection = one TCP to :443). These
	// ceilings still drop an egregious single-IP flood (thousands of connections /
	// hundreds of new conns per second) while leaving shared NAT egress alone. Lower
	// them per install via ROSPANEL_CONNLIMIT_MAX / ROSPANEL_CONNLIMIT_RATE.
	defaultMaxConnPerIP = 1500 // concurrent new TCP connections tracked per source IP
	defaultNewConnRate  = 300  // new connections/second per source IP
	defaultNewConnBurst = 600  // token-bucket burst for the rate limiter
)

// Limits holds the tunable per-IP caps.
type Limits struct {
	MaxConnPerIP int
	NewConnRate  int
	NewConnBurst int
}

// DefaultLimits returns the baked-in defaults.
func DefaultLimits() Limits {
	return Limits{
		MaxConnPerIP: defaultMaxConnPerIP,
		NewConnRate:  defaultNewConnRate,
		NewConnBurst: defaultNewConnBurst,
	}
}

// Ruleset returns the nftables ruleset guarding the given TCP ports. Dynamic sets
// keyed on the source address track per-IP state; IPv4 and IPv6 need separate
// sets because their element types differ, so each guard is expressed twice (the
// `ip`/`ip6 saddr` match makes each rule apply to only its family).
func Ruleset(tcpPorts []int, lim Limits) string {
	portList := joinPorts(tcpPorts)
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", tableName)
	b.WriteString("\tset rate4 { type ipv4_addr; flags dynamic; }\n")
	b.WriteString("\tset rate6 { type ipv6_addr; flags dynamic; }\n")
	b.WriteString("\tset conn4 { type ipv4_addr; flags dynamic; }\n")
	b.WriteString("\tset conn6 { type ipv6_addr; flags dynamic; }\n")
	b.WriteString("\tchain input {\n")
	// priority filter - 5 runs ahead of the default filter hook (and the panel's
	// iptables INPUT rules at priority 0), so a flood is dropped early; policy
	// accept means anything not matched is left entirely untouched.
	b.WriteString("\t\ttype filter hook input priority filter - 5; policy accept;\n")
	// Never limit loopback: internal health checks / local probes to the proxy port
	// must not be throttled, and a single 127.0.0.1 source could otherwise exhaust
	// the per-IP budget for everyone behind it.
	b.WriteString("\t\tiif \"lo\" accept\n")
	// New-connection rate cap per source IP.
	fmt.Fprintf(&b, "\t\ttcp dport { %s } ct state new add @rate4 { ip saddr limit rate over %d/second burst %d packets } drop\n",
		portList, lim.NewConnRate, lim.NewConnBurst)
	fmt.Fprintf(&b, "\t\ttcp dport { %s } ct state new add @rate6 { ip6 saddr limit rate over %d/second burst %d packets } drop\n",
		portList, lim.NewConnRate, lim.NewConnBurst)
	// Concurrent-connection cap per source IP.
	fmt.Fprintf(&b, "\t\ttcp dport { %s } ct state new add @conn4 { ip saddr ct count over %d } drop\n",
		portList, lim.MaxConnPerIP)
	fmt.Fprintf(&b, "\t\ttcp dport { %s } ct state new add @conn6 { ip6 saddr ct count over %d } drop\n",
		portList, lim.MaxConnPerIP)
	b.WriteString("\t}\n}\n")
	return b.String()
}

// Active reports whether our nftables table is currently loaded — i.e. whether the
// per-IP limits are really in force. Ensure is best-effort and degrades to a no-op
// (no nft binary, not Linux, not root), so "we called Ensure at boot" is not
// evidence that the guard exists; only asking nft is.
func Active() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := exec.LookPath("nft"); err != nil {
		return false
	}
	return exec.Command("nft", "list", "table", "inet", tableName).Run() == nil
}

// Ensure (re)installs the connection-guard rules for the given TCP ports. Ports
// ≤0 are ignored; with no valid ports the existing table is removed (a clean
// disable). Best-effort: returns nil (after logging) when nft is unavailable or
// off Linux.
func Ensure(tcpPorts []int, lim Limits) error {
	if runtime.GOOS != "linux" {
		log.Printf("connguard: skipping nftables setup on %s (per-IP connection limits only apply on the Linux server)", runtime.GOOS)
		return nil
	}
	if _, err := exec.LookPath("nft"); err != nil {
		log.Printf("connguard: nft not found in PATH; per-IP connection limits not configured (install nftables)")
		return nil
	}
	ports := validPorts(tcpPorts)
	// Drop any prior version of our table first so a reconfigure (or a disable)
	// always starts from a known-clean state.
	_ = exec.Command("nft", "delete", "table", "inet", tableName).Run()
	if len(ports) == 0 {
		log.Printf("connguard: no public TCP ports to guard — connection limits disabled")
		return nil
	}
	if lim.MaxConnPerIP <= 0 || lim.NewConnRate <= 0 || lim.NewConnBurst <= 0 {
		lim = DefaultLimits()
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(Ruleset(ports, lim))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft load failed: %w\n%s", err, out)
	}
	log.Printf("connguard: per-IP limits installed on tcp %s (max %d concurrent, %d new/s burst %d)",
		joinPorts(ports), lim.MaxConnPerIP, lim.NewConnRate, lim.NewConnBurst)
	return nil
}

func validPorts(ports []int) []int {
	var out []int
	seen := map[int]bool{}
	for _, p := range ports {
		if p > 0 && p < 65536 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func joinPorts(ports []int) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ", ")
}
