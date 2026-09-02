// Package ipblock drops traffic from individual addresses at the firewall
// (nftables). Each Blocker owns its OWN table, which is what keeps the panel's
// several firewall users out of each other's way: connguard deletes and rebuilds
// its table wholesale on every reconfigure, and a blocked address living in that
// table would go with it. A Blocker's table is created once and only ever has
// addresses added to / removed from its sets, so a block survives unrelated
// firewall changes.
//
// Linux + nftables only; every call degrades to a logged no-op elsewhere, exactly
// like connguard, so the caller need not special-case the platform. A nil Blocker
// is a working no-op too — a panel built without one (the tests) blocks nothing
// rather than crashing.
package ipblock

import (
	"fmt"
	"log"
	"net/netip"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Table names in use. Each is a separate nftables table with its own drop chain,
// so switching one feature off cannot lift the other's blocks.
const (
	// TableProbes holds addresses caught scanning for the hidden panel path.
	TableProbes = "rospanel_probeblock"
	// TablePolicy holds addresses refused by the source policy (country / network).
	TablePolicy = "rospanel_policyblock"
)

// DefaultTTL is how long a blocked address stays in the kernel set. The sets carry
// `flags timeout` so each element self-expires, which bounds the set (a public IP is
// scanned by a constant churn of distinct bots) instead of growing forever — a
// re-offending address is simply re-blocked when it next crosses the line.
const DefaultTTL = 24 * time.Hour

// ensureRetryCooldown backs off ensure() after it fails, so a box where nft is present but
// every command fails (non-root, no nf_tables module, netlink EPERM) doesn't fork+exec nft
// and log an error once per crossing address — it would drown the log ring.
const ensureRetryCooldown = 5 * time.Minute

// Blocker is one nftables table's worth of blocked addresses.
type Blocker struct {
	table string
	ttl   time.Duration

	// mu serializes every nft mutation and guards armed/ensureFailedAt. Blocks are
	// fired from a goroutine per address, so without this two first-time blocks could
	// both pass ensure's check-then-act and load the ruleset twice — and `add rule`
	// APPENDS (it is not idempotent), so the drop rules would be duplicated.
	mu sync.Mutex
	// armed gates BlockIP. Clear() (the operator switching the feature off) disarms, so
	// an in-flight BlockIP that read a now-stale "enabled" setting can't ensure() the
	// table back into existence after it was torn down; Arm() re-arms. Default true so a
	// fresh boot with the feature on blocks immediately without an explicit Arm().
	armed          bool
	ensureFailedAt time.Time
}

// New returns a Blocker for one table, with the default block lifetime.
func New(table string) *Blocker { return &Blocker{table: table, ttl: DefaultTTL, armed: true} }

// WithTTL returns a Blocker whose blocks expire after d (0 keeps the default).
func (b *Blocker) WithTTL(d time.Duration) *Blocker {
	if b == nil || d <= 0 {
		return b
	}
	b.mu.Lock()
	b.ttl = d
	b.mu.Unlock()
	return b
}

// ruleset creates the table, the two address sets, and an input-hook chain that drops
// any source in them. The sets carry `flags timeout` so blocks self-expire (see the TTL).
// The `add table`/`add set`/`add chain` statements are idempotent, but `add rule` appends
// — so it must be applied exactly once (guarded by mu + the table-exists check in ensure),
// never re-run against an existing table.
func ruleset(table string) string {
	return fmt.Sprintf(`add table inet %[1]s
add set inet %[1]s blocked4 { type ipv4_addr; flags timeout; }
add set inet %[1]s blocked6 { type ipv6_addr; flags timeout; }
add chain inet %[1]s input { type filter hook input priority -5; policy accept; }
add rule inet %[1]s input iif "lo" accept
add rule inet %[1]s input ip saddr @blocked4 drop
add rule inet %[1]s input ip6 saddr @blocked6 drop
`, table)
}

// Available reports whether this host can block at all (Linux with nft installed).
func Available() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("nft")
	return err == nil
}

// ensure creates the table/sets/chain if the table isn't there yet. A no-op once it
// exists, so it never re-adds the drop rules or disturbs the blocked set — unless the
// table predates the `flags timeout` sets (an older deploy), in which case it is rebuilt
// once so blocks self-expire instead of accumulating forever.
func (b *Blocker) ensure() error {
	if out, err := exec.Command("nft", "list", "table", "inet", b.table).CombinedOutput(); err == nil {
		if strings.Contains(string(out), "flags timeout") {
			return nil // already installed with self-expiring sets
		}
		// Pre-timeout table from an older build — drop it so it comes back with timeouts.
		_ = exec.Command("nft", "delete", "table", "inet", b.table).Run()
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset(b.table))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft install %s table: %w\n%s", b.table, err, out)
	}
	log.Printf("ipblock: nftables drop table installed (%s)", b.table)
	return nil
}

// setFor returns the set name for an address family, or "" if the address is invalid.
func setFor(ip string) (string, netip.Addr, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", netip.Addr{}, false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return "blocked4", addr, true
	}
	return "blocked6", addr, true
}

// BlockIP drops all traffic from ip at the firewall. Best-effort and idempotent: a
// missing nft, a non-Linux host, or an already-blocked IP are not errors the caller
// needs to handle.
func (b *Blocker) BlockIP(ip string) error {
	if b == nil || !Available() {
		return nil
	}
	set, addr, ok := setFor(ip)
	if !ok {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.armed {
		return nil // blocking was switched off; don't resurrect the table
	}
	if !b.ensureFailedAt.IsZero() && time.Since(b.ensureFailedAt) < ensureRetryCooldown {
		return nil // nft is failing on this box; back off instead of a per-address storm
	}
	if err := b.ensure(); err != nil {
		b.ensureFailedAt = time.Now()
		return err
	}
	elem := fmt.Sprintf("{ %s timeout %s }", addr.String(), b.ttl)
	out, err := exec.Command("nft", "add", "element", "inet", b.table, set, elem).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		b.ensureFailedAt = time.Now()
		return fmt.Errorf("nft add element: %w\n%s", err, out)
	}
	b.ensureFailedAt = time.Time{} // a successful add proves nft works; clear the backoff
	return nil
}

// UnblockIP lifts a block. Best-effort; an IP that isn't blocked is not an error.
func (b *Blocker) UnblockIP(ip string) error {
	if b == nil || !Available() {
		return nil
	}
	set, addr, ok := setFor(ip)
	if !ok {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	elem := fmt.Sprintf("{ %s }", addr.String())
	out, err := exec.Command("nft", "delete", "element", "inet", b.table, set, elem).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such file") {
		return fmt.Errorf("nft delete element: %w\n%s", err, out)
	}
	return nil
}

// Clear removes the whole table (used when the feature is switched off, so nothing
// stays blocked at the firewall after the operator disables it).
func (b *Blocker) Clear() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.armed = false // disarm first, so a racing in-flight BlockIP can't rebuild the table
	if !Available() {
		return nil
	}
	_ = exec.Command("nft", "delete", "table", "inet", b.table).Run()
	return nil
}

// Arm re-enables blocking after a Clear(), called when the operator switches the
// feature back on. BlockIP stays a no-op between a Clear() and the next Arm().
func (b *Blocker) Arm() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.armed = true
	b.mu.Unlock()
}

// Sync makes the kernel set hold exactly `ips` — what a node does with the list the
// panel pushed it. Addresses the panel no longer blocks are lifted, new ones added;
// an address already there keeps its own expiry rather than being re-armed, so a
// resend does not extend a block that was about to lapse.
func (b *Blocker) Sync(ips []string) error {
	if b == nil || !Available() {
		return nil
	}
	want := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		if _, addr, ok := setFor(ip); ok {
			want[addr.String()] = struct{}{}
		}
	}
	if len(want) == 0 {
		// Nothing should be blocked. Tear the table down rather than leave an empty
		// one: a node whose policy was switched off must not keep a drop chain.
		b.mu.Lock()
		defer b.mu.Unlock()
		if !Available() {
			return nil
		}
		_ = exec.Command("nft", "delete", "table", "inet", b.table).Run()
		return nil
	}
	have, err := b.blocked()
	if err != nil {
		return err
	}
	for ip := range have {
		if _, keep := want[ip]; !keep {
			if err := b.UnblockIP(ip); err != nil {
				return err
			}
		}
	}
	for ip := range want {
		if _, already := have[ip]; already {
			continue
		}
		if err := b.BlockIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// blocked reads the addresses currently in the kernel sets.
func (b *Blocker) blocked() (map[string]struct{}, error) {
	out := map[string]struct{}{}
	raw, err := exec.Command("nft", "-j", "list", "table", "inet", b.table).CombinedOutput()
	if err != nil {
		// No table yet is the normal first-run case, not a failure.
		return out, nil
	}
	// The JSON carries the elements as bare strings inside each set; picking them out
	// with a scan avoids modelling nft's whole schema for two arrays of addresses.
	for _, field := range strings.FieldsFunc(string(raw), func(r rune) bool {
		return r == '"' || r == ',' || r == '[' || r == ']' || r == '{' || r == '}' || r == ' ' || r == '\n'
	}) {
		if addr, err := netip.ParseAddr(field); err == nil {
			out[addr.Unmap().String()] = struct{}{}
		}
	}
	return out, nil
}
