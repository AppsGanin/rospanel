// Package shaper caps how fast individual users may move traffic, using the
// kernel's own scheduler (HTB via tc) keyed on the addresses those users are
// currently connected from.
//
// Xray has no per-user bandwidth limit and is unlikely to grow one — the feature
// requests are years old, and the panels that offer it either patch the core into
// their own binary or do what this does. Since the panel runs Xray as a released
// binary, the cap has to live below it, in the kernel.
//
// What that buys and what it costs:
//
//   - It is enforced for every protocol at once, including QUIC, because it acts on
//     packets rather than on sessions Xray knows about.
//   - It is keyed on ADDRESS, not on account. Two users behind one CGNAT address
//     share one cap, and a user on two addresses gets the cap on each. The address
//     set comes from the access-log tap, so it follows a roaming client within a
//     minute or so.
//   - Hysteria2 uses Brutal congestion control, which by design ignores packet loss.
//     A shaped Hysteria2 client does not slow down politely; it keeps sending and
//     the excess is dropped. The cap holds, but the user's experience of hitting it
//     is packet loss rather than a slower download. Say so in the UI.
//
// Everything here is best-effort in the same sense as internal/connguard: on a host
// without tc, without root, or off Linux, Apply logs and returns nil rather than
// failing the panel. A cap nobody can install must not stop the panel from serving.
package shaper

import (
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
)

// ifbDev is the virtual device the WAN's INGRESS traffic is mirrored to. Uplink
// cannot be shaped on the WAN interface itself — the kernel's ingress hook can only
// police (drop), while an HTB queue needs an egress path — so the standard trick is
// to redirect ingress to an IFB device and shape that device's egress.
const ifbDev = "ifb-rospanel"

// classOffset keeps our minor class ids away from 1:1, the default class every
// unshaped packet lands in.
const classOffset = 0x10

// maxClasses bounds how many users can be shaped at once. A tc minor id is 16 bits
// and the filter list is walked linearly per packet, so the ceiling is a real one;
// it sits far above any single-box install.
const maxClasses = 4000

// defaultRateKbit is the ceiling of the unshaped default class. HTB requires a rate
// on every class, and this one exists only to hold traffic that no filter matched —
// so it is set high enough to be no limit on any link this panel runs on.
const defaultRateKbit = 10_000_000 // 10 Gbit/s

// Rule is one user's cap and the addresses it applies to.
type Rule struct {
	UserID int64
	Kbps   int      // both directions; 0 or less means "not shaped"
	IPs    []string // source addresses the user is currently connected from
}

// State is the desired shaping for one host.
type State struct {
	// WAN is the interface facing the internet — the one a user's packets arrive on
	// and leave through.
	WAN   string
	Rules []Rule
}

// Commands renders the tc/ip invocations that put `st` in force, in order.
//
// The whole tree is rebuilt rather than diffed. A diff would have to track class and
// filter identity across passes for a gain that does not exist here: the desired
// state changes only when an operator edits a limit or a user appears from a new
// address, which is rare, and Apply skips identical states entirely. Rebuilding is
// also the only version of this that is obviously correct after a partial failure.
//
// Returns nil when nothing is shaped — the caller then tears down instead.
func Commands(st State) [][]string {
	rules := shapeable(st.Rules)
	if st.WAN == "" || len(rules) == 0 {
		return nil
	}
	var out [][]string
	add := func(args ...string) { out = append(out, args) }

	// ── Downlink: HTB on the WAN's egress, matched on destination address ────────
	add("tc", "qdisc", "replace", "dev", st.WAN, "root", "handle", "1:", "htb", "default", "1")
	add("tc", "class", "replace", "dev", st.WAN, "parent", "1:", "classid", "1:1",
		"htb", "rate", kbit(defaultRateKbit))
	// Everyone who ISN'T capped lands in 1:1, so it needs a real queue too. Without
	// this it gets HTB's default pfifo — a single dumb FIFO for all unshaped traffic,
	// which is a bufferbloat regression for users the operator never meant to touch.
	// Handle 9: and not 1: — the root qdisc already owns 1:, and a child claiming the
	// same major is rejected.
	add("tc", "qdisc", "replace", "dev", st.WAN, "parent", "1:1", "handle", "9:", "fq_codel")

	// ── Uplink: mirror the WAN's ingress onto an IFB device and shape that ──────
	add("ip", "link", "add", ifbDev, "type", "ifb")
	add("ip", "link", "set", ifbDev, "up")
	add("tc", "qdisc", "replace", "dev", st.WAN, "handle", "ffff:", "ingress")
	add("tc", "filter", "replace", "dev", st.WAN, "parent", "ffff:", "protocol", "all",
		"prio", "10", "u32", "match", "u32", "0", "0",
		"action", "mirred", "egress", "redirect", "dev", ifbDev)
	add("tc", "qdisc", "replace", "dev", ifbDev, "root", "handle", "1:", "htb", "default", "1")
	add("tc", "class", "replace", "dev", ifbDev, "parent", "1:", "classid", "1:1",
		"htb", "rate", kbit(defaultRateKbit))
	add("tc", "qdisc", "replace", "dev", ifbDev, "parent", "1:1", "handle", "9:", "fq_codel")

	if len(rules) > maxClasses {
		// Never silently: past this many the rest of the fleet is simply not shaped,
		// and an operator reading "the limit doesn't work" deserves the reason to be
		// in the log rather than in this file.
		slog.Warn("shaper: too many capped users — the excess is NOT shaped",
			"capped", len(rules), "max", maxClasses)
	}
	for i, r := range rules {
		if i >= maxClasses {
			break
		}
		class := classID(i)
		for _, dev := range []string{st.WAN, ifbDev} {
			add("tc", "class", "replace", "dev", dev, "parent", "1:", "classid", "1:"+class,
				"htb", "rate", kbit(r.Kbps), "ceil", kbit(r.Kbps), "burst", "32k")
			// fq_codel under each class so one greedy stream inside the cap doesn't
			// starve the user's own interactive traffic. Without a leaf qdisc HTB uses
			// pfifo, which is a single dumb queue.
			add("tc", "qdisc", "replace", "dev", dev, "parent", "1:"+class,
				"handle", class+":", "fq_codel")
		}
		for _, ip := range r.IPs {
			proto, keyword := "ip", "dst"
			if isV6(ip) {
				proto, keyword = "ipv6", "dst"
			}
			// Downlink: packets HEADED FOR the user's address.
			add("tc", "filter", "add", "dev", st.WAN, "protocol", proto, "parent", "1:",
				"prio", "1", "u32", "match", matchField(proto), keyword, ip, "flowid", "1:"+class)
			// Uplink: packets FROM it, seen on the mirrored ingress.
			add("tc", "filter", "add", "dev", ifbDev, "protocol", proto, "parent", "1:",
				"prio", "1", "u32", "match", matchField(proto), "src", ip, "flowid", "1:"+class)
		}
	}
	return out
}

// TeardownCommands removes everything Commands installs. Safe to run when nothing
// is installed: each invocation fails independently and the caller ignores the
// error, which is why they are separate commands rather than one chain.
func TeardownCommands(wan string) [][]string {
	var out [][]string
	if wan != "" {
		out = append(out,
			[]string{"tc", "qdisc", "del", "dev", wan, "root"},
			[]string{"tc", "qdisc", "del", "dev", wan, "ingress"},
		)
	}
	return append(out, []string{"ip", "link", "del", ifbDev})
}

// shapeable filters and orders the rules that will actually produce classes: a cap
// of zero is "unlimited", and a user with no known address has nothing to match on.
//
// The order is by user id, so the class a user gets is stable across passes for an
// unchanged rule set — which is what makes the state hash meaningful.
func shapeable(rules []Rule) []Rule {
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		ips := validIPs(r.IPs)
		if r.Kbps <= 0 || len(ips) == 0 {
			continue
		}
		r.IPs = ips
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out
}

// validIPs keeps only parseable addresses, deduped and sorted.
//
// These strings come from parsing Xray's access log, i.e. from something a client
// influences, and they are about to be handed to a command line. Parsing them is
// what guarantees no argument can be anything but an address — the exec never goes
// through a shell, but "no shell" is not a reason to pass through unvalidated text.
func validIPs(ips []string) []string {
	seen := make(map[string]struct{}, len(ips))
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		addr := net.ParseIP(strings.TrimSpace(ip))
		if addr == nil {
			continue
		}
		s := addr.String()
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func isV6(ip string) bool {
	addr := net.ParseIP(ip)
	return addr != nil && addr.To4() == nil
}

// matchField is the u32 selector family: "ip" for v4, "ip6" for v6.
func matchField(proto string) string {
	if proto == "ipv6" {
		return "ip6"
	}
	return "ip"
}

func classID(i int) string { return strconv.FormatInt(int64(classOffset+i), 16) }

func kbit(k int) string { return strconv.Itoa(k) + "kbit" }

// Hash fingerprints a desired state so an unchanged one is never re-applied. It is
// derived from exactly what Commands reads, so equal hashes mean identical trees.
func Hash(st State) string {
	var b strings.Builder
	b.WriteString(st.WAN)
	for _, r := range shapeable(st.Rules) {
		fmt.Fprintf(&b, "|%d:%d:%s", r.UserID, r.Kbps, strings.Join(r.IPs, ","))
	}
	return b.String()
}
