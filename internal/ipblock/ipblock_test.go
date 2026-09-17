package ipblock

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSetFor(t *testing.T) {
	cases := []struct {
		ip  string
		set string
		ok  bool
	}{
		{"1.2.3.4", "blocked4", true},
		{"2a02:6b8::1", "blocked6", true},
		{"::ffff:1.2.3.4", "blocked4", true}, // v4-mapped unmaps to v4
		{"not-an-ip", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		set, _, ok := setFor(c.ip)
		if set != c.set || ok != c.ok {
			t.Errorf("setFor(%q) = (%q, %v), want (%q, %v)", c.ip, set, ok, c.set, c.ok)
		}
	}
}

// On a host without nft (CI/dev, and any non-Linux box) every action is a logged
// no-op that returns nil, so callers never have to special-case the platform.
func TestBestEffortNoError(t *testing.T) {
	if Available() {
		t.Skip("nft present; the no-op path isn't exercised here")
	}
	b := New(TableProbes)
	if err := b.BlockIP("1.2.3.4"); err != nil {
		t.Errorf("BlockIP no-op returned %v", err)
	}
	if err := b.UnblockIP("1.2.3.4"); err != nil {
		t.Errorf("UnblockIP no-op returned %v", err)
	}
	if err := b.Sync([]string{"1.2.3.4"}); err != nil {
		t.Errorf("Sync no-op returned %v", err)
	}
	if err := b.Clear(); err != nil {
		t.Errorf("Clear no-op returned %v", err)
	}
}

// A panel built without a blocker (every test manager) must block nothing rather
// than crash — the same contract the shaper has.
func TestNilBlockerIsANoOp(t *testing.T) {
	var b *Blocker
	if err := b.BlockIP("1.2.3.4"); err != nil {
		t.Errorf("BlockIP: %v", err)
	}
	if err := b.UnblockIP("1.2.3.4"); err != nil {
		t.Errorf("UnblockIP: %v", err)
	}
	if err := b.Sync(nil); err != nil {
		t.Errorf("Sync: %v", err)
	}
	if err := b.Clear(); err != nil {
		t.Errorf("Clear: %v", err)
	}
	b.Arm()
	if got := b.WithTTL(0); got != nil {
		t.Errorf("WithTTL on nil returned %v", got)
	}
}

// Each purpose gets its own table, which is what keeps switching one feature off
// from lifting the other's blocks.
func TestTablesAreDistinct(t *testing.T) {
	if TableProbes == TablePolicy {
		t.Fatal("the probe and policy blockers must not share a table")
	}
	if got := ruleset(TablePolicy, false); !contains(got, TablePolicy) || contains(got, TableProbes) {
		t.Errorf("ruleset names the wrong table:\n%s", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// A table of self-expiring blocks and a permanent one differ only in the sets' flags.
func TestRulesetPermanent(t *testing.T) {
	timed, permanent := ruleset("t", false), ruleset("t", true)
	if !strings.Contains(timed, "{ type ipv4_addr; flags timeout; }") {
		t.Errorf("timed ruleset:\n%s", timed)
	}
	if strings.Contains(permanent, "timeout") || !strings.Contains(permanent, "{ type ipv4_addr; }") {
		t.Errorf("permanent ruleset:\n%s", permanent)
	}
	for _, s := range []string{timed, permanent} {
		if !strings.Contains(s, "ip saddr @blocked4 drop") || !strings.Contains(s, "ip6 saddr @blocked6 drop") {
			t.Errorf("no drop rules:\n%s", s)
		}
	}
}

// A table is left alone only when it is the shape this blocker installs; anything else
// is rebuilt, so a permanent table cannot be stuck as a timed one or half-built.
func TestInstalledShape(t *testing.T) {
	timed, permanent := ruleset("t", false), ruleset("t", true)
	// `nft list table` prints the sets and rules much as they were added.
	for name, c := range map[string]struct {
		listing   string
		permanent bool
		want      bool
	}{
		"timed as timed":         {timed, false, true},
		"permanent as permanent": {permanent, true, true},
		"timed as permanent":     {timed, true, false},
		"permanent as timed":     {permanent, false, false},
		"no drop rules":          {"table inet t { set blocked4 { type ipv4_addr; } }", true, false},
	} {
		if got := installedShape(c.listing, c.permanent); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// The elements come out of `nft -j` output as nftables 1.0.9 prints them: with a
// timeout and what is left of it, or bare in a set without timeouts.
func TestParseEntries(t *testing.T) {
	raw := []byte(`{"nftables": [{"metainfo": {"version": "1.0.9"}},
{"table": {"family": "inet", "name": "x", "handle": 71}},
{"set": {"family": "inet", "name": "blocked4", "table": "x", "type": "ipv4_addr", "flags": ["timeout"],
  "elem": [{"elem": {"val": "39.109.109.136", "timeout": 86400, "expires": 76411}}]}},
{"set": {"family": "inet", "name": "blocked6", "table": "x", "type": "ipv6_addr", "elem": ["2001:db8::1", "::ffff:1.2.3.4"]}},
{"set": {"family": "inet", "name": "empty", "table": "x", "type": "ipv4_addr"}},
{"chain": {"family": "inet", "table": "x", "name": "input"}}]}`)
	got, err := parseEntries(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{IP: "39.109.109.136", Expires: 76411 * time.Second},
		{IP: "2001:db8::1"},
		{IP: "1.2.3.4"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("entries = %+v, want %+v", got, want)
	}
}
