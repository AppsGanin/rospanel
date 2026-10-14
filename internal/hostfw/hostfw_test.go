package hostfw

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// fakeIPT is iptables' filter table as the commands we use see it.
type fakeIPT struct {
	policy map[string]string
	rules  map[string][]string // chain → "-A chain …" lines minus the prefix
	writes int
}

func newFakeIPT(inputPolicy string, input ...string) *fakeIPT {
	return &fakeIPT{
		policy: map[string]string{"INPUT": inputPolicy, "FORWARD": "ACCEPT", "OUTPUT": "ACCEPT"},
		rules:  map[string][]string{"INPUT": input, "FORWARD": nil, "OUTPUT": nil},
	}
}

func (f *fakeIPT) run(args ...string) (string, error) {
	op, chain := args[0], args[1]
	rest := strings.Join(args[2:], " ")
	_, exists := f.rules[chain]
	switch op {
	case "-S":
		if !exists {
			return "iptables: No chain/target/match by that name.", errors.New("exit 1")
		}
		var b strings.Builder
		if p, builtin := f.policy[chain]; builtin {
			fmt.Fprintf(&b, "-P %s %s\n", chain, p)
		} else {
			fmt.Fprintf(&b, "-N %s\n", chain)
		}
		for _, r := range f.rules[chain] {
			fmt.Fprintf(&b, "-A %s %s\n", chain, r)
		}
		return b.String(), nil
	case "-C":
		if exists && slices.Contains(f.rules[chain], rest) {
			return "", nil
		}
		return "", errors.New("exit 1")
	}
	switch op {
	case "-N":
		if exists {
			return "Chain already exists.", errors.New("exit 1")
		}
		f.rules[chain] = nil
	case "-F":
		if !exists {
			return "", errors.New("exit 1")
		}
		f.rules[chain] = nil
	case "-X":
		if !exists {
			return "", errors.New("exit 1")
		}
		delete(f.rules, chain)
	case "-A":
		f.rules[chain] = append(f.rules[chain], rest)
	case "-I": // -I chain 1 rule…
		f.rules[chain] = append([]string{strings.Join(args[3:], " ")}, f.rules[chain]...)
	case "-D":
		i := slices.Index(f.rules[chain], rest)
		if i < 0 {
			return "", errors.New("exit 1")
		}
		f.rules[chain] = slices.Delete(f.rules[chain], i, i+1)
	default:
		return "", fmt.Errorf("fake iptables: %v", args)
	}
	f.writes++
	return "", nil
}

// withHost stands fakes in for iptables (both families share one table here) and,
// when fw is non-nil, firewalld.
func withHost(t *testing.T, ipt *fakeIPT, fw *fakeFirewalld) {
	t.Helper()
	oldRun, oldLook, oldLast, oldOwned := run, lookPath, last, owned
	run = func(name string, args ...string) (string, error) {
		switch name {
		case "iptables":
			return ipt.run(args...)
		case "ip6tables":
			return "", errors.New("not here")
		case "firewall-cmd":
			return fw.run(args...)
		}
		return "", fmt.Errorf("unexpected %s", name)
	}
	lookPath = func(name string) (string, error) {
		if name == "firewall-cmd" && fw == nil || name == "ip6tables" {
			return "", errors.New("not found")
		}
		return "/usr/sbin/" + name, nil
	}
	last = nil
	owned = firewalldOwned{ports: map[string]map[Port]bool{}, ifaces: map[string]bool{}}
	t.Cleanup(func() { run, lookPath, last, owned = oldRun, oldLook, oldLast, oldOwned })
}

func TestIptablesUntouchedWhenInputAcceptsAll(t *testing.T) {
	ipt := newFakeIPT("ACCEPT")
	withHost(t, ipt, nil)
	applyLocked(Spec{Ports: []Port{TCP(443)}, Forward: []string{"awg0"}}.normalize())
	if ipt.writes != 0 {
		t.Fatalf("an accepting host was written to: %v", ipt.rules)
	}
}

func TestIptablesOpensAheadOfUfw(t *testing.T) {
	ipt := newFakeIPT("DROP", "-j ufw-before-input", "-j ufw-user-input")
	ipt.policy["FORWARD"] = "DROP" // Docker
	ipt.rules["FORWARD"] = []string{"-j DOCKER-USER"}
	withHost(t, ipt, nil)
	s := Spec{Ports: []Port{UDP(443), TCP(443), {Proto: "udp", From: 20000, To: 20010}}, Forward: []string{"awg0"}}.normalize()
	applyLocked(s)

	if ipt.rules["INPUT"][0] != "-j "+inChain {
		t.Fatalf("INPUT does not jump to us first: %v", ipt.rules["INPUT"])
	}
	want := []string{
		"-p tcp -m tcp --dport 443 -j ACCEPT",
		"-p udp -m udp --dport 443 -j ACCEPT",
		"-p udp -m udp --dport 20000:20010 -j ACCEPT",
	}
	if !slices.Equal(ipt.rules[inChain], want) {
		t.Fatalf("%s = %v, want %v", inChain, ipt.rules[inChain], want)
	}
	if ipt.rules["FORWARD"][0] != "-j "+fwdChain || len(ipt.rules[fwdChain]) != 2 {
		t.Fatalf("FORWARD not opened for the tunnel: %v / %v", ipt.rules["FORWARD"], ipt.rules[fwdChain])
	}

	// The same spec again writes nothing.
	n := ipt.writes
	applyLocked(s)
	if ipt.writes != n {
		t.Fatalf("re-applying an unchanged spec wrote %d times", ipt.writes-n)
	}

	// ufw re-inserting its own jump on top: ours goes back to the head, once.
	ipt.rules["INPUT"] = append([]string{"-j ufw-before-logging-input"}, ipt.rules["INPUT"]...)
	applyLocked(s)
	if ipt.rules["INPUT"][0] != "-j "+inChain || strings.Count(strings.Join(ipt.rules["INPUT"], "\n"), inChain) != 1 {
		t.Fatalf("jump not moved back to the head: %v", ipt.rules["INPUT"])
	}

	// The tunnel off and the ports gone: everything of ours is taken back.
	applyLocked(Spec{}.normalize())
	if _, ok := ipt.rules[inChain]; ok {
		t.Fatalf("%s left behind", inChain)
	}
	if _, ok := ipt.rules[fwdChain]; ok {
		t.Fatalf("%s left behind", fwdChain)
	}
	if !slices.Equal(ipt.rules["INPUT"], []string{"-j ufw-before-logging-input", "-j ufw-before-input", "-j ufw-user-input"}) {
		t.Fatalf("the operator's INPUT changed: %v", ipt.rules["INPUT"])
	}
}

func TestIptablesCleansUpWhenFirewallGoesAway(t *testing.T) {
	ipt := newFakeIPT("DROP", "-j ufw-before-input")
	withHost(t, ipt, nil)
	s := Spec{Ports: []Port{TCP(443)}}.normalize()
	applyLocked(s)
	// `ufw disable`: policy back to ACCEPT, its jumps gone, ours left.
	ipt.policy["INPUT"] = "ACCEPT"
	ipt.rules["INPUT"] = []string{"-j " + inChain}
	applyLocked(s)
	if len(ipt.rules["INPUT"]) != 0 {
		t.Fatalf("our jump outlived the firewall: %v", ipt.rules["INPUT"])
	}
}

func TestIptablesRewritesAChainChangedUnderUs(t *testing.T) {
	ipt := newFakeIPT("DROP")
	withHost(t, ipt, nil)
	s := Spec{Ports: []Port{TCP(443), UDP(51820)}}.normalize()
	applyLocked(s)
	ipt.rules[inChain] = ipt.rules[inChain][:1] // someone flushed half of it
	applyLocked(s)
	if len(ipt.rules[inChain]) != 2 {
		t.Fatalf("chain not restored: %v", ipt.rules[inChain])
	}
}

// fakeFirewalld is firewall-cmd's runtime view.
type fakeFirewalld struct {
	zones  map[string]map[string]bool // zone → "443/tcp"
	ifaces map[string]string          // interface → zone
	def    string
	calls  int
}

func (f *fakeFirewalld) run(args ...string) (string, error) {
	f.calls++
	zone := ""
	var rest []string
	for _, a := range args {
		if z, ok := strings.CutPrefix(a, "--zone="); ok {
			zone = z
		} else {
			rest = append(rest, a)
		}
	}
	switch {
	case rest[0] == "--state":
		return "running\n", nil
	case rest[0] == "--get-default-zone":
		return f.def + "\n", nil
	case rest[0] == "--get-active-zones":
		var b strings.Builder
		for i, z := range f.ifaces {
			fmt.Fprintf(&b, "%s\n  interfaces: %s\n", z, i)
		}
		return b.String(), nil
	case rest[0] == "--list-ports":
		var ps []string
		for p := range f.zones[zone] {
			ps = append(ps, p)
		}
		return strings.Join(ps, " ") + "\n", nil
	case strings.HasPrefix(rest[0], "--get-zone-of-interface="):
		if z, ok := f.ifaces[strings.TrimPrefix(rest[0], "--get-zone-of-interface=")]; ok {
			return z + "\n", nil
		}
		return "no zone\n", errors.New("exit 2")
	}
	for _, a := range rest {
		k, v, _ := strings.Cut(a, "=")
		switch k {
		case "--add-port":
			if f.zones[zone] == nil {
				f.zones[zone] = map[string]bool{}
			}
			f.zones[zone][v] = true
		case "--remove-port":
			delete(f.zones[zone], v)
		case "--add-interface", "--change-interface":
			f.ifaces[v] = zone
		case "--remove-interface":
			delete(f.ifaces, v)
		default:
			return "", fmt.Errorf("fake firewall-cmd: %v", args)
		}
	}
	return "success\n", nil
}

func TestFirewalldOpensAndTakesBackOnlyItsOwn(t *testing.T) {
	fw := &fakeFirewalld{
		zones:  map[string]map[string]bool{"public": {"22/tcp": true, "443/tcp": true}},
		ifaces: map[string]string{"eth0": "public"},
		def:    "public",
	}
	withHost(t, newFakeIPT("ACCEPT"), fw)

	applyLocked(Spec{Ports: []Port{TCP(443), UDP(443), UDP(51820)}, Forward: []string{"awg0"}}.normalize())
	for _, p := range []string{"22/tcp", "443/tcp", "443/udp", "51820/udp"} {
		if !fw.zones["public"][p] {
			t.Fatalf("%s not open: %v", p, fw.zones["public"])
		}
	}
	if fw.ifaces["awg0"] != trustedZone {
		t.Fatalf("awg0 in %q, want trusted", fw.ifaces["awg0"])
	}

	// AmneziaWG off, Hysteria2 off: our ports close, the operator's 443/tcp stays —
	// it was open before we came.
	applyLocked(Spec{Ports: []Port{TCP(443)}}.normalize())
	if fw.zones["public"]["443/udp"] || fw.zones["public"]["51820/udp"] {
		t.Fatalf("our ports left open: %v", fw.zones["public"])
	}
	if !fw.zones["public"]["443/tcp"] || !fw.zones["public"]["22/tcp"] {
		t.Fatalf("the operator's ports were closed: %v", fw.zones["public"])
	}
	if _, ok := fw.ifaces["awg0"]; ok {
		t.Fatal("awg0 left in the trusted zone")
	}

	// A firewalld reload drops runtime ports; the next apply puts them back.
	applyLocked(Spec{Ports: []Port{TCP(443), UDP(51820)}}.normalize())
	delete(fw.zones["public"], "51820/udp")
	applyLocked(Spec{Ports: []Port{TCP(443), UDP(51820)}}.normalize())
	if !fw.zones["public"]["51820/udp"] {
		t.Fatal("port not restored after a reload")
	}
}

func TestParse(t *testing.T) {
	zones := parseActiveZones("public (default)\n  interfaces: eth0\ndocker\n  interfaces: docker0\n")
	if !slices.Equal(zones, []string{"public", "docker"}) {
		t.Fatalf("zones = %v", zones)
	}
	ports := parsePorts("443/tcp 1000-2000/udp junk\n")
	if !ports[TCP(443)] || !ports[Port{Proto: "udp", From: 1000, To: 2000}] || len(ports) != 2 {
		t.Fatalf("ports = %v", ports)
	}
	n := Spec{Ports: []Port{UDP(443), TCP(443), TCP(443), {Proto: "sctp", From: 1, To: 1}, TCP(0)}, Forward: []string{"awg0", "awg0", ""}}.normalize()
	if !slices.Equal(n.Ports, []Port{TCP(443), UDP(443)}) || !slices.Equal(n.Forward, []string{"awg0"}) {
		t.Fatalf("normalize = %+v", n)
	}
}

func TestFirewalldTakesBackFromAZoneThatWentInactive(t *testing.T) {
	fw := &fakeFirewalld{
		zones:  map[string]map[string]bool{},
		ifaces: map[string]string{"eth0": "work"},
		def:    "public",
	}
	withHost(t, newFakeIPT("ACCEPT"), fw)
	s := Spec{Ports: []Port{UDP(51820)}}.normalize()
	applyLocked(s)
	if !fw.zones["work"]["51820/udp"] || !fw.zones["public"]["51820/udp"] {
		t.Fatalf("not opened in the active and default zones: %v", fw.zones)
	}

	fw.ifaces["eth0"] = "internal" // the operator moved the interface
	applyLocked(s)
	if fw.zones["work"]["51820/udp"] {
		t.Fatalf("port left in the inactive zone: %v", fw.zones["work"])
	}
	if !fw.zones["internal"]["51820/udp"] {
		t.Fatalf("not opened in the new zone: %v", fw.zones)
	}
	calls := fw.calls
	applyLocked(s)
	// Settled: work is no longer visited, nothing is written.
	if fw.calls-calls > 5 {
		t.Fatalf("a settled spec made %d firewall-cmd calls", fw.calls-calls)
	}
}

func TestManagedIgnoresWarnings(t *testing.T) {
	listing := "# Warning: iptables-legacy tables present, use iptables-legacy to see them\n-P INPUT ACCEPT\n"
	if managed(listing, "INPUT", inChain) {
		t.Fatal("a warning line made an accepting chain look managed")
	}
	if !managed("-P INPUT DROP\n", "INPUT", inChain) || !managed("-P INPUT ACCEPT\n-A INPUT -j ufw-before-input\n", "INPUT", inChain) {
		t.Fatal("a filtering chain looked unmanaged")
	}
}
