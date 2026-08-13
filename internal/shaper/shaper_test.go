package shaper

import (
	"strings"
	"testing"
)

// joined renders the command list as text, so a test can ask what the kernel is
// being told without matching argument slices by hand.
func joined(cmds [][]string) string {
	var b strings.Builder
	for _, c := range cmds {
		b.WriteString(strings.Join(c, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestCommandsShapeBothDirections(t *testing.T) {
	got := joined(Commands(State{
		WAN:   "eth0",
		Rules: []Rule{{UserID: 7, Kbps: 20000, IPs: []string{"1.2.3.4"}}},
	}))

	for _, want := range []string{
		"tc qdisc replace dev eth0 root handle 1: htb default 1",
		"tc qdisc replace dev eth0 handle ffff: ingress",
		"redirect dev " + ifbDev,
		"tc class replace dev eth0 parent 1: classid 1:10 htb rate 20000kbit ceil 20000kbit",
		"tc class replace dev " + ifbDev + " parent 1: classid 1:10 htb rate 20000kbit",
		// Downlink matches where the packet is going, uplink where it came from.
		"tc filter add dev eth0 protocol ip parent 1: prio 1 u32 match ip dst 1.2.3.4 flowid 1:10",
		"tc filter add dev " + ifbDev + " protocol ip parent 1: prio 1 u32 match ip src 1.2.3.4 flowid 1:10",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCommandsIPv6UsesIP6Selectors(t *testing.T) {
	got := joined(Commands(State{
		WAN:   "eth0",
		Rules: []Rule{{UserID: 1, Kbps: 1000, IPs: []string{"2001:db8::1"}}},
	}))
	if !strings.Contains(got, "protocol ipv6 parent 1: prio 1 u32 match ip6 dst 2001:db8::1") {
		t.Errorf("v6 downlink filter missing:\n%s", got)
	}
	if strings.Contains(got, "match ip dst 2001:db8::1") {
		t.Error("a v6 address was matched with a v4 selector")
	}
}

// A rule with no cap, no known address, or an unparseable one produces nothing:
// those are the three ways the caller can hand us something unshapeable.
func TestCommandsSkipsUnshapeableRules(t *testing.T) {
	cmds := Commands(State{WAN: "eth0", Rules: []Rule{
		{UserID: 1, Kbps: 0, IPs: []string{"1.2.3.4"}},
		{UserID: 2, Kbps: 5000},
		{UserID: 3, Kbps: 5000, IPs: []string{"not-an-ip", "$(reboot)"}},
	}})
	if cmds != nil {
		t.Errorf("expected no commands, got:\n%s", joined(cmds))
	}
}

// Addresses reach a command line, so anything that isn't one must be dropped before
// it gets there — the access log is not a trusted source.
func TestValidIPsDropsGarbageAndDedupes(t *testing.T) {
	got := validIPs([]string{"1.2.3.4", " 1.2.3.4 ", "10.0.0.1; rm -rf /", "", "::1"})
	want := []string{"1.2.3.4", "::1"}
	if len(got) != len(want) {
		t.Fatalf("validIPs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("validIPs = %v, want %v", got, want)
		}
	}
}

// Class ids must be stable for an unchanged rule set, or every pass would reshuffle
// which user is capped by which class.
func TestClassAssignmentIsStable(t *testing.T) {
	st := State{WAN: "eth0", Rules: []Rule{
		{UserID: 9, Kbps: 1000, IPs: []string{"10.0.0.9"}},
		{UserID: 2, Kbps: 2000, IPs: []string{"10.0.0.2"}},
	}}
	first := joined(Commands(st))
	// Same rules, opposite order — the output must not move.
	st.Rules[0], st.Rules[1] = st.Rules[1], st.Rules[0]
	if second := joined(Commands(st)); first != second {
		t.Errorf("rule order changed the tree:\n%s\n---\n%s", first, second)
	}
	if !strings.Contains(first, "match ip dst 10.0.0.2 flowid 1:10") {
		t.Error("the lowest user id didn't get the first class")
	}
}

func TestHashIgnoresOrderAndUnshapeableRules(t *testing.T) {
	a := Hash(State{WAN: "eth0", Rules: []Rule{
		{UserID: 1, Kbps: 100, IPs: []string{"1.1.1.1"}},
		{UserID: 2, Kbps: 0, IPs: []string{"2.2.2.2"}},
	}})
	b := Hash(State{WAN: "eth0", Rules: []Rule{
		{UserID: 2, Kbps: 0, IPs: []string{"2.2.2.2"}},
		{UserID: 1, Kbps: 100, IPs: []string{"1.1.1.1"}},
	}})
	if a != b {
		t.Errorf("hash differs for the same effective state:\n%s\n%s", a, b)
	}
	c := Hash(State{WAN: "eth0", Rules: []Rule{
		{UserID: 1, Kbps: 200, IPs: []string{"1.1.1.1"}},
	}})
	if a == c {
		t.Error("hash unchanged after the cap changed")
	}
}

func TestTeardownAlwaysRemovesTheIFB(t *testing.T) {
	got := joined(TeardownCommands(""))
	if !strings.Contains(got, "ip link del "+ifbDev) {
		t.Errorf("teardown leaves the IFB device behind:\n%s", got)
	}
}

func TestParseDefaultDev(t *testing.T) {
	for in, want := range map[string]string{
		"default via 10.0.0.1 dev eth0 proto static metric 100":         "eth0",
		"default via fe80::1 dev ens3 proto ra metric 1024 pref medium": "ens3",
		"":                                "",
		"10.0.0.0/24 dev eth0 scope link": "",
	} {
		if got := parseDefaultDev(in); got != want {
			t.Errorf("parseDefaultDev(%q) = %q, want %q", in, got, want)
		}
	}
}
