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
	"strings"
)

// tableName is our dedicated nftables table so installs are idempotent: we own
// it entirely and recreate it wholesale.
const tableName = "rospanel_hop"

// Ruleset returns the nftables ruleset that redirects UDP hopStart..hopEnd to
// target. It lives in its own `inet rospanel_hop` table.
func Ruleset(hopStart, hopEnd, target int) string {
	return fmt.Sprintf(`table inet %s {
	chain prerouting {
		type nat hook prerouting priority dstnat; policy accept;
		udp dport %d-%d redirect to :%d
	}
}
`, tableName, hopStart, hopEnd, target)
}

// Ensure (re)installs the hop rules. It's a best-effort no-op when nft is
// unavailable or the OS isn't Linux (e.g. local dev on macOS) — the panel must
// not fail to start just because hopping can't be configured here.
func Ensure(hopStart, hopEnd, target int) error {
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

	// Only the ports ABOVE the base listener need redirecting onto it; the base
	// itself is delivered directly (a base→base self-redirect is pointless and can
	// confuse NAT). With no such ports (a single-port setup, e.g. UDP 443), there's
	// nothing to hop, so leave no rule.
	start := hopStart
	if start <= target {
		start = target + 1
	}
	if start > hopEnd {
		log.Printf("hop: single port %d — no port-hopping rule needed", target)
		return nil
	}

	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(Ruleset(start, hopEnd, target))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft load failed: %w\n%s", err, out)
	}
	log.Printf("hop: nftables redirect %d-%d → :%d installed", start, hopEnd, target)
	return nil
}
