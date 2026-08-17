// Package probeblock drops traffic from IPs caught scanning for the hidden panel
// path, at the firewall (nftables). It owns its OWN table so it is independent of
// connguard — connguard deletes and rebuilds its table wholesale on every reconfigure,
// which would wipe any elements added to it. This table is created once and only ever
// has individual addresses added to / removed from its sets, so a blocked IP survives
// unrelated firewall changes.
//
// Linux + nftables only; every call degrades to a logged no-op elsewhere, exactly like
// connguard, so the caller need not special-case the platform.
package probeblock

import (
	"fmt"
	"log"
	"net/netip"
	"os/exec"
	"runtime"
	"strings"
)

const tableName = "rospanel_probeblock"

// ruleset creates the table, the two address sets, and an input-hook chain that drops
// any source in them. Uses only `add` (idempotent) so re-running it never duplicates a
// rule; it is applied once, when the table doesn't exist yet.
const ruleset = `add table inet rospanel_probeblock
add set inet rospanel_probeblock blocked4 { type ipv4_addr; }
add set inet rospanel_probeblock blocked6 { type ipv6_addr; }
add chain inet rospanel_probeblock input { type filter hook input priority -5; policy accept; }
add rule inet rospanel_probeblock input iif "lo" accept
add rule inet rospanel_probeblock input ip saddr @blocked4 drop
add rule inet rospanel_probeblock input ip6 saddr @blocked6 drop
`

func available() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("nft")
	return err == nil
}

// ensure creates the table/sets/chain if the table isn't there yet. A no-op once it
// exists, so it never re-adds the drop rules or disturbs the blocked set.
func ensure() error {
	if exec.Command("nft", "list", "table", "inet", tableName).Run() == nil {
		return nil // already installed
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft install probeblock table: %w\n%s", err, out)
	}
	log.Printf("probeblock: nftables drop table installed (%s)", tableName)
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
func BlockIP(ip string) error {
	if !available() {
		return nil
	}
	set, addr, ok := setFor(ip)
	if !ok {
		return nil
	}
	if err := ensure(); err != nil {
		return err
	}
	elem := fmt.Sprintf("{ %s }", addr.String())
	out, err := exec.Command("nft", "add", "element", "inet", tableName, set, elem).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		return fmt.Errorf("nft add element: %w\n%s", err, out)
	}
	return nil
}

// UnblockIP lifts a block. Best-effort; an IP that isn't blocked is not an error.
func UnblockIP(ip string) error {
	if !available() {
		return nil
	}
	set, addr, ok := setFor(ip)
	if !ok {
		return nil
	}
	elem := fmt.Sprintf("{ %s }", addr.String())
	out, err := exec.Command("nft", "delete", "element", "inet", tableName, set, elem).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such file") {
		return fmt.Errorf("nft delete element: %w\n%s", err, out)
	}
	return nil
}

// Clear removes the whole table (used when auto-blocking is switched off, so nothing
// stays blocked at the firewall after the operator disables it).
func Clear() error {
	if !available() {
		return nil
	}
	_ = exec.Command("nft", "delete", "table", "inet", tableName).Run()
	return nil
}
