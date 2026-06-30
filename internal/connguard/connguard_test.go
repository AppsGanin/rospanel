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
