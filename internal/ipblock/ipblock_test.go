package ipblock

import "testing"

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
	if got := ruleset(TablePolicy); !contains(got, TablePolicy) || contains(got, TableProbes) {
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
