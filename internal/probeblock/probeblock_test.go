package probeblock

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
	if available() {
		t.Skip("nft present; the no-op path isn't exercised here")
	}
	if err := BlockIP("1.2.3.4"); err != nil {
		t.Errorf("BlockIP no-op returned %v", err)
	}
	if err := UnblockIP("1.2.3.4"); err != nil {
		t.Errorf("UnblockIP no-op returned %v", err)
	}
	if err := Clear(); err != nil {
		t.Errorf("Clear no-op returned %v", err)
	}
}
