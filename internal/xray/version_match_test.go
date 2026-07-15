package xray

import "testing"

func TestVersionMatchesPinned(t *testing.T) {
	// The reported version (from `xray version`) has no "v"; PinnedVersion does.
	for _, v := range []string{"26.6.27", "v26.6.27"} {
		if !VersionMatchesPinned(v) {
			t.Errorf("VersionMatchesPinned(%q) = false, want true (pinned=%s)", v, PinnedVersion)
		}
	}
	if VersionMatchesPinned("26.6.26") {
		t.Error("a genuinely different version must not match")
	}
}
