// Package hostfw opens the server's own listeners in a host firewall the operator
// runs — ufw or any iptables INPUT that drops, firewalld — and lets the AmneziaWG
// tunnel's traffic through FORWARD. The panel's nftables tables cannot do this: an
// accept in one table does not outvote another table's drop on the same hook, a
// packet has to pass every base chain. So a server with ufw on drops a Hysteria2 or
// AmneziaWG client before any of our rules is asked, and a server with Docker
// (FORWARD policy DROP) lets an AmneziaWG client handshake and then reach nothing.
//
// Nothing is touched on a host whose chains accept everything already, and the
// panel installs no firewall of its own. Best-effort throughout: a missing tool or a
// failing command is logged and the rest goes on.
//
// Hysteria2's hop ranges need no rule: nftables redirects them onto the Hysteria
// port in prerouting, before any input chain sees the packet.
package hostfw

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Port is a listener to let in: one port, or From-To inclusive.
type Port struct {
	Proto    string // "tcp" or "udp"
	From, To int
}

// TCP and UDP name one port.
func TCP(p int) Port { return Port{Proto: "tcp", From: p, To: p} }
func UDP(p int) Port { return Port{Proto: "udp", From: p, To: p} }

func (p Port) String() string {
	if p.To > p.From {
		return fmt.Sprintf("%d-%d/%s", p.From, p.To, p.Proto)
	}
	return fmt.Sprintf("%d/%s", p.From, p.Proto)
}

// Spec is everything this server wants open: its listeners, and the interfaces whose
// traffic it routes on (the AmneziaWG tunnel).
type Spec struct {
	Ports   []Port
	Forward []string
}

// normalize sorts, drops invalid ports and duplicates, so equal specs compare equal
// and the rules come out in a stable order.
func (s Spec) normalize() Spec {
	var out Spec
	seen := map[Port]bool{}
	for _, p := range s.Ports {
		if p.To < p.From {
			p.To = p.From
		}
		if (p.Proto != "tcp" && p.Proto != "udp") || p.From < 1 || p.To > 65535 || seen[p] {
			continue
		}
		seen[p] = true
		out.Ports = append(out.Ports, p)
	}
	sort.Slice(out.Ports, func(i, j int) bool {
		a, b := out.Ports[i], out.Ports[j]
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		return a.From < b.From || a.From == b.From && a.To < b.To
	})
	for _, f := range s.Forward {
		if f != "" && !slices.Contains(out.Forward, f) {
			out.Forward = append(out.Forward, f)
		}
	}
	sort.Strings(out.Forward)
	return out
}

// cmdTimeout bounds one firewall command: firewall-cmd waits on D-Bus, and a hung
// firewalld must not hold up the reconcile that called Ensure.
const cmdTimeout = 20 * time.Second

// run executes a firewall command; a variable so tests can stand in for the host.
var run = func(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

var lookPath = exec.LookPath

var (
	mu      sync.Mutex
	last    *Spec
	lastErr string
	// owned is what this process added to firewalld, so it takes back only that and
	// never a port the operator opened by hand. Lost on restart, which leaves at most
	// a runtime rule until firewalld reloads.
	owned = firewalldOwned{ports: map[string]map[Port]bool{}, ifaces: map[string]bool{}}
)

// Disabled reports whether the operator switched this off (ROSPANEL_HOSTFW=off). Off
// in tests too: a reconcile under `go test` as root must not rewrite the developer's
// firewall.
func Disabled() bool {
	return strings.EqualFold(os.Getenv("ROSPANEL_HOSTFW"), "off") || testing.Testing()
}

// Ensure brings the host firewall in line with s and remembers it for Keep. The spec
// already in force is not applied again — a user change reaches here too, and Keep is
// what catches the firewall moving under us.
func Ensure(s Spec) {
	if runtime.GOOS != "linux" || Disabled() {
		return
	}
	s = s.normalize()
	mu.Lock()
	defer mu.Unlock()
	if last != nil && last.equal(s) {
		return
	}
	last = &s
	applyLocked(s)
}

func (s Spec) equal(o Spec) bool {
	return slices.Equal(s.Ports, o.Ports) && slices.Equal(s.Forward, o.Forward)
}

// Keep re-applies the last spec every interval until ctx ends: `ufw reload`,
// `firewall-cmd --reload` and a firewall restarted under us all drop what we added.
func Keep(ctx context.Context, every time.Duration) {
	if runtime.GOOS != "linux" || Disabled() {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			mu.Lock()
			if last != nil {
				applyLocked(*last)
			}
			mu.Unlock()
		}
	}
}

func applyLocked(s Spec) {
	var errs []string
	for _, bin := range []string{"iptables", "ip6tables"} {
		if _, err := lookPath(bin); err != nil {
			continue
		}
		if err := syncChain(bin, "INPUT", inChain, inputRules(s.Ports)); err != nil {
			errs = append(errs, err.Error())
		}
		if err := syncChain(bin, "FORWARD", fwdChain, forwardRules(s.Forward)); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if _, err := lookPath("firewall-cmd"); err == nil {
		if err := syncFirewalld(s); err != nil {
			errs = append(errs, err.Error())
		}
	}
	// Said once per change, not on every Keep tick.
	if msg := strings.Join(errs, "; "); msg != lastErr {
		lastErr = msg
		if msg != "" {
			slog.Warn("hostfw: could not open the server's ports in the host firewall", "err", msg)
		}
	}
}

// --- iptables (ufw, Docker, a hand-written INPUT/FORWARD) ---------------------

// Our own chains, jumped to from the head of the built-in one: a sync rewrites them
// without touching anything the operator or ufw put there.
const (
	inChain  = "ROSPANEL-IN"
	fwdChain = "ROSPANEL-FWD"
)

func inputRules(ports []Port) [][]string {
	var out [][]string
	for _, p := range ports {
		dport := fmt.Sprint(p.From)
		if p.To > p.From {
			dport = fmt.Sprintf("%d:%d", p.From, p.To)
		}
		out = append(out, []string{"-p", p.Proto, "-m", p.Proto, "--dport", dport, "-j", "ACCEPT"})
	}
	return out
}

func forwardRules(ifaces []string) [][]string {
	var out [][]string
	for _, i := range ifaces {
		out = append(out,
			[]string{"-i", i, "-j", "ACCEPT"},
			[]string{"-o", i, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"})
	}
	return out
}

// managed reports whether a built-in chain, as `iptables -S` lists it, filters
// anything: a policy other than ACCEPT or a rule that is not our own jump. A bare
// accepting chain lets everything through without our help.
func managed(listing, builtin, own string) bool {
	for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue // iptables-nft's "# Warning: iptables-legacy tables present"
		}
		switch line {
		case "", "-P " + builtin + " ACCEPT", "-A " + builtin + " -j " + own:
		default:
			return true
		}
	}
	return false
}

// syncChain makes own hold exactly rules and the head of builtin jump to it — or, when
// builtin filters nothing or there is nothing to let in, takes both away.
func syncChain(bin, builtin, own string, rules [][]string) error {
	listing, err := run(bin, "-S", builtin)
	if err != nil {
		return fmt.Errorf("%s -S %s: %v: %s", bin, builtin, err, strings.TrimSpace(listing))
	}
	if len(rules) == 0 || !managed(listing, builtin, own) {
		removeChain(bin, builtin, own, listing)
		return nil
	}
	if !chainHolds(bin, own, rules) {
		_, _ = run(bin, "-N", own) // already there is fine; -F below fails if not
		if out, err := run(bin, "-F", own); err != nil {
			return fmt.Errorf("%s -F %s: %v: %s", bin, own, err, strings.TrimSpace(out))
		}
		for _, r := range rules {
			if out, err := run(bin, append([]string{"-A", own}, r...)...); err != nil {
				return fmt.Errorf("%s -A %s %s: %v: %s", bin, own, strings.Join(r, " "), err, strings.TrimSpace(out))
			}
		}
		slog.Info("hostfw: opened in "+bin, "chain", builtin, "rules", len(rules))
	}
	// First, so a drop or a reject the operator placed above us cannot win; a jump
	// that slid down (ufw re-inserting its own at the top) is moved back.
	if !jumpFirst(listing, builtin, own) {
		for i := 0; i < 8; i++ {
			if _, err := run(bin, "-D", builtin, "-j", own); err != nil {
				break
			}
		}
		if out, err := run(bin, "-I", builtin, "1", "-j", own); err != nil {
			return fmt.Errorf("%s -I %s: %v: %s", bin, builtin, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// chainHolds reports whether own already is exactly rules.
func chainHolds(bin, own string, rules [][]string) bool {
	listing, err := run(bin, "-S", own)
	if err != nil {
		return false
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
		if strings.HasPrefix(line, "-A ") {
			n++
		}
	}
	if n != len(rules) {
		return false
	}
	for _, r := range rules {
		if _, err := run(bin, append([]string{"-C", own}, r...)...); err != nil {
			return false
		}
	}
	return true
}

// jumpFirst reports whether the first rule of builtin is the jump to own.
func jumpFirst(listing, builtin, own string) bool {
	for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
		if strings.HasPrefix(line, "-A ") {
			return strings.TrimSpace(line) == "-A "+builtin+" -j "+own
		}
	}
	return false
}

func removeChain(bin, builtin, own, listing string) {
	for i := 0; i < 8 && strings.Contains(listing, " -j "+own); i++ {
		if _, err := run(bin, "-D", builtin, "-j", own); err != nil {
			break
		}
	}
	if _, err := run(bin, "-S", own); err != nil {
		return // never created
	}
	_, _ = run(bin, "-F", own)
	if _, err := run(bin, "-X", own); err == nil {
		slog.Info("hostfw: closed in "+bin, "chain", builtin)
	}
}

// --- firewalld ----------------------------------------------------------------

type firewalldOwned struct {
	ports  map[string]map[Port]bool // zone → ports we added there
	ifaces map[string]bool          // interfaces we put into the trusted zone
}

// trustedZone takes the tunnel: a zone whose target is ACCEPT lets its interfaces'
// traffic in and on through forwarding.
const trustedZone = "trusted"

// syncFirewalld opens the ports in every active zone (and the default one, which
// takes interfaces assigned to none) as runtime rules, and puts each forwarding
// interface into the trusted zone. Runtime only: nothing outlives the panel past a
// firewalld reload, and Keep puts it back after one.
func syncFirewalld(s Spec) error {
	if out, err := run("firewall-cmd", "--state"); err != nil || strings.TrimSpace(out) != "running" {
		return nil // installed but stopped, or not reachable over D-Bus: nothing to open
	}
	active, err := firewalldZones()
	if err != nil {
		return err
	}
	// A zone that went inactive still holds what we opened there: it is visited to
	// take that back, and gets nothing new.
	zones := slices.Clone(active)
	for z, mine := range owned.ports {
		if len(mine) > 0 && !slices.Contains(zones, z) {
			zones = append(zones, z)
		}
	}
	want := map[Port]bool{}
	for _, p := range s.Ports {
		want[p] = true
	}
	var errs []string
	for _, z := range zones {
		if z == trustedZone {
			continue // accepts everything already
		}
		out, err := run("firewall-cmd", "--zone="+z, "--list-ports")
		if err != nil {
			errs = append(errs, fmt.Sprintf("list-ports %s: %v", z, err))
			continue
		}
		open := parsePorts(out)
		mine := owned.ports[z]
		if mine == nil {
			mine = map[Port]bool{}
			owned.ports[z] = mine
		}
		var add, del []string
		for _, p := range s.Ports {
			if !open[p] && slices.Contains(active, z) {
				add = append(add, "--add-port="+p.String())
				mine[p] = true
			}
		}
		for p := range mine {
			if !want[p] || !slices.Contains(active, z) {
				if open[p] {
					del = append(del, "--remove-port="+p.String())
				}
				delete(mine, p)
			}
		}
		for _, args := range [][]string{add, del} {
			if len(args) == 0 {
				continue
			}
			if out, err := run("firewall-cmd", append([]string{"--zone=" + z}, args...)...); err != nil {
				errs = append(errs, fmt.Sprintf("%s in %s: %v: %s", strings.Join(args, " "), z, err, strings.TrimSpace(out)))
			} else {
				slog.Info("hostfw: firewalld updated", "zone", z, "change", strings.Join(args, " "))
			}
		}
	}
	for _, i := range s.Forward {
		zone, _ := run("firewall-cmd", "--get-zone-of-interface="+i)
		if strings.TrimSpace(zone) == trustedZone {
			continue
		}
		verb := "--add-interface=" + i
		if z := strings.TrimSpace(zone); z != "" && !strings.Contains(z, " ") {
			verb = "--change-interface=" + i // held by another zone: move it
		}
		if out, err := run("firewall-cmd", "--zone="+trustedZone, verb); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v: %s", verb, err, strings.TrimSpace(out)))
			continue
		}
		owned.ifaces[i] = true
	}
	for i := range owned.ifaces {
		if !slices.Contains(s.Forward, i) {
			_, _ = run("firewall-cmd", "--zone="+trustedZone, "--remove-interface="+i)
			delete(owned.ifaces, i)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("firewalld: %s", strings.Join(errs, "; "))
	}
	return nil
}

// firewalldZones is the active zones plus the default one.
func firewalldZones() ([]string, error) {
	out, err := run("firewall-cmd", "--get-active-zones")
	if err != nil && strings.TrimSpace(out) != "" && !strings.Contains(out, "no active zones") {
		return nil, fmt.Errorf("firewalld: get-active-zones: %v: %s", err, strings.TrimSpace(out))
	}
	zones := parseActiveZones(out)
	if def, err := run("firewall-cmd", "--get-default-zone"); err == nil {
		if d := strings.TrimSpace(def); d != "" && !slices.Contains(zones, d) {
			zones = append(zones, d)
		}
	}
	return zones, nil
}

// parseActiveZones reads `firewall-cmd --get-active-zones`: a zone name at the start
// of a line, its interfaces and sources indented under it. Newer firewalld adds
// " (default)" or " (active)" after the name.
func parseActiveZones(out string) []string {
	var zones []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		if f := strings.Fields(line); len(f) > 0 && !slices.Contains(zones, f[0]) {
			zones = append(zones, f[0])
		}
	}
	return zones
}

// parsePorts reads `firewall-cmd --list-ports`: "443/tcp 1000-2000/udp".
func parsePorts(out string) map[Port]bool {
	open := map[Port]bool{}
	for _, f := range strings.Fields(out) {
		num, proto, ok := strings.Cut(f, "/")
		if !ok {
			continue
		}
		var p Port
		p.Proto = proto
		if a, b, isRange := strings.Cut(num, "-"); isRange {
			if _, err := fmt.Sscan(a, &p.From); err != nil {
				continue
			}
			if _, err := fmt.Sscan(b, &p.To); err != nil {
				continue
			}
		} else {
			if _, err := fmt.Sscan(num, &p.From); err != nil {
				continue
			}
			p.To = p.From
		}
		open[p] = true
	}
	return open
}
