package xray

import (
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
