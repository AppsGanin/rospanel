package hop

import (
	"strings"
	"testing"
)

// The base port is delivered to the listener directly, so a range that overlaps it
// must lose everything at or below it — a base→base redirect confuses NAT — and a
// range entirely below the base has nothing left to funnel.
func TestNormalizeDropsPortsAtOrBelowTheTarget(t *testing.T) {
	cases := []struct {
		in   Range
		want Range
		ok   bool
	}{
		{Range{20000, 30000, 443}, Range{20000, 30000, 443}, true},
		{Range{443, 500, 443}, Range{444, 500, 443}, true},          // starts on the base
		{Range{100, 500, 443}, Range{444, 500, 443}, true},          // straddles the base
		{Range{444, 444, 443}, Range{444, 444, 443}, true},          // a single port is fine
		{Range{100, 443, 443}, Range{444, 443, 443}, false},         // nothing above the base
		{Range{30000, 20000, 443}, Range{30000, 20000, 443}, false}, // inverted
	}
	for _, tc := range cases {
		got, ok := tc.in.normalize()
		if ok != tc.ok || got != tc.want {
			t.Errorf("normalize(%+v) = %+v, %v; want %+v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// The ruleset is what nft -f loads verbatim: one dedicated table (so it can be
// dropped and recreated wholesale), one prerouting NAT chain, one redirect per
// funnel, in the order given.
func TestRulesetAllRendersEveryFunnelInOneChain(t *testing.T) {
	got := RulesetAll([]Range{{20000, 30000, 443}, {40000, 40100, 8443}})
	want := "table inet rospanel_hop {\n" +
		"\tchain prerouting {\n" +
		"\t\ttype nat hook prerouting priority dstnat; policy accept;\n" +
		"\t\tudp dport 20000-30000 redirect to :443\n" +
		"\t\tudp dport 40000-40100 redirect to :8443\n" +
		"\t}\n}\n"
	if got != want {
		t.Errorf("RulesetAll =\n%s\nwant\n%s", got, want)
	}
	if strings.Count(got, "table inet") != 1 || strings.Count(got, "chain prerouting") != 1 {
		t.Error("every funnel must share one table and one chain, or the last apply erases the rest")
	}
}

// No funnels still renders a complete, loadable table: EnsureAll returns before
// calling nft in that case, but the renderer must not emit a fragment either.
func TestRulesetAllWithNothingIsStillWellFormed(t *testing.T) {
	got := RulesetAll(nil)
	if !strings.HasPrefix(got, "table inet rospanel_hop {\n") || !strings.HasSuffix(got, "\t}\n}\n") {
		t.Errorf("empty ruleset = %q", got)
	}
	if strings.Contains(got, "udp dport") {
		t.Error("a rule appeared for no range")
	}
}
