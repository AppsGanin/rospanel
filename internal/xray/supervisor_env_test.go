package xray

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// Xray is started with the ceiling MemoryLimit names, because whoever starts it sets
// exactly that much of the box aside for it when sizing their own limit
// (tuning.SetMemoryLimit). The two drifting apart is how a panel and its Xray come to
// be promised more memory than the machine has.
func TestXrayIsStartedWithTheCeilingItIsGiven(t *testing.T) {
	var got string
	for _, kv := range (&Supervisor{}).env() {
		if v, ok := strings.CutPrefix(kv, "GOMEMLIMIT="); ok {
			got = v
		}
	}
	if want := strconv.FormatInt(MemoryLimit, 10); got != want {
		t.Fatalf("xray runs with GOMEMLIMIT=%q, want %q", got, want)
	}
}

func TestXrayTCPDefaultIgnoresInheritedMPTCP(t *testing.T) {
	for _, tc := range []struct{ name, parent, want string }{
		{"empty", "", "multipathtcp=0"},
		{"enabled", "multipathtcp=1", "multipathtcp=0"},
		{"other options", "http2debug=1,multipathtcp=1,asynctimerchan=1", "http2debug=1,asynctimerchan=1,multipathtcp=0"},
		{"duplicates", "multipathtcp=0,http2debug=1,multipathtcp=1", "http2debug=1,multipathtcp=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GODEBUG", tc.parent)
			var values []string
			for _, kv := range (&Supervisor{}).env() {
				if value, ok := strings.CutPrefix(kv, "GODEBUG="); ok {
					values = append(values, value)
				}
			}
			if len(values) != 1 || values[0] != tc.want {
				t.Fatalf("child GODEBUG = %q, want one %q", values, tc.want)
			}
			if got := os.Getenv("GODEBUG"); got != tc.parent {
				t.Fatalf("parent GODEBUG changed to %q", got)
			}
		})
	}
}
