// Package hop manages Hysteria2 UDP port-hopping via host NAT. Server-side
// hopping is NOT a protocol feature — it's a kernel redirect of a UDP port
// range onto the single Hysteria2 listener. The client sprays across the range;
// nftables funnels it all to one port.
package hop

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

// tableName is our dedicated nftables table so installs are idempotent: we own
// it entirely and recreate it wholesale.
const tableName = "rospanel_hop"

// Range is one funnel: UDP Start..End redirected onto Target.
type Range struct {
	Start, End, Target int
}

// normalize drops the ports at or below the target (the base port is delivered
// directly — a base→base self-redirect is pointless and can confuse NAT) and
// reports whether anything is left to redirect.
func (r Range) normalize() (Range, bool) {
	if r.Start <= r.Target {
		r.Start = r.Target + 1
	}
	return r, r.Start <= r.End
}

// RulesetAll renders one table holding every funnel. All ranges live in a single
// chain and are loaded in one `nft -f`, because the table is recreated wholesale on
// each apply: rendering them separately would mean the last writer erased the rest.
func RulesetAll(ranges []Range) string {
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n\tchain prerouting {\n", tableName)
	b.WriteString("\t\ttype nat hook prerouting priority dstnat; policy accept;\n")
	for _, r := range ranges {
		fmt.Fprintf(&b, "\t\tudp dport %d-%d redirect to :%d\n", r.Start, r.End, r.Target)
	}
	b.WriteString("\t}\n}\n")
	return b.String()
}

// EnsureAll (re)installs the hop rules for every funnel on this host — the built-in
// Hysteria2 lane plus any custom Hysteria2 inbound that asks for hopping.
//
// The whole set is rewritten in one shot rather than range by range: our table is
// dropped and recreated on every apply, so an incremental writer would silently
// delete the funnels it wasn't told about. Overlaps are rejected before this point
// (model.ValidateInboundSet), so ordering within the chain doesn't matter.
//
// Best-effort no-op when nft is unavailable or the OS isn't Linux (e.g. local dev on
// macOS) — the panel must not fail to start just because hopping can't be configured
// here.
func EnsureAll(ranges []Range) error {
	if runtime.GOOS != "linux" {
		log.Printf("hop: skipping nftables setup on %s (host-NAT hopping only applies on the Linux server)", runtime.GOOS)
		return nil
	}
	if _, err := exec.LookPath("nft"); err != nil {
		log.Printf("hop: nft not found in PATH; port-hopping not configured (install nftables)")
		return nil
	}
	// Drop any prior version of our table, then load fresh (idempotent).
	_ = exec.Command("nft", "delete", "table", "inet", tableName).Run()

	var live []Range
	for _, r := range ranges {
		if n, ok := r.normalize(); ok {
			live = append(live, n)
		}
	}
	if len(live) == 0 {
		log.Printf("hop: no port-hopping ranges configured")
		return nil
	}
	// Deterministic order so an unchanged configuration produces an identical
	// ruleset (and identical logs) across restarts.
	sort.Slice(live, func(i, j int) bool { return live[i].Start < live[j].Start })

	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(RulesetAll(live))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft load failed: %w\n%s", err, out)
	}
	for _, r := range live {
		log.Printf("hop: nftables redirect %d-%d → :%d installed", r.Start, r.End, r.Target)
	}
	return nil
}
