package connguard

import "strings"

import "testing"

func TestRulesetContainsGuards(t *testing.T) {
	rs := Ruleset([]int{443, 8443}, DefaultLimits())
	for _, want := range []string{
		"table inet rospanel_connlimit",
		"type filter hook input priority filter - 5",
		"iif \"lo\" accept",
		"tcp dport { 443, 8443 }",
		"ip saddr ct count over 1500",
		"ip6 saddr ct count over 1500",
		"ip saddr limit rate over 300/second burst 600 packets",
		"ip6 saddr limit rate over 300/second burst 600 packets",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset missing %q\n---\n%s", want, rs)
		}
	}
}

func TestValidPortsDedupAndFilter(t *testing.T) {
	got := validPorts([]int{443, 0, 443, -1, 70000, 60000})
	want := []int{443, 60000}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

// A cascade exit leaves its entries alone: each carries all its users from one
// address, and the per-IP caps would cut every one of them.
func TestRulesetExemptsCascadeEntries(t *testing.T) {
	lim := DefaultLimits()
	lim.Exempt = []string{"203.0.113.7", "2001:db8::5", "::ffff:198.51.100.9", "bogus"}
	rs := Ruleset([]int{443}, lim)
	v4 := strings.Index(rs, "ip saddr { 203.0.113.7, 198.51.100.9 } accept")
	v6 := strings.Index(rs, "ip6 saddr { 2001:db8::5 } accept")
	limit := strings.Index(rs, "limit rate over")
	if v4 < 0 || v6 < 0 || limit < 0 || v4 > limit || v6 > limit {
		t.Fatalf("exemptions missing or after the limits:\n%s", rs)
	}
	if strings.Contains(Ruleset([]int{443}, DefaultLimits()), "saddr {") {
		t.Fatal("an exemption with nothing to exempt")
	}
}
