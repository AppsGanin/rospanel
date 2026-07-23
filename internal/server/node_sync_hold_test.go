package server

import (
	"testing"
	"time"
)

// A hold pinned to exactly 45s turns the panel↔node link into a flat line at one
// frequency — the payload is opaque but the schedule is not, and nothing a person
// does looks like that. The hold must stay inside the agent's 90s syncTimeout at
// the top and stay useful at the bottom.
func TestNodeSyncHoldIsJitteredWithinBounds(t *testing.T) {
	const (
		lo = (nodeSyncHoldSec - nodeSyncHoldJitter) * time.Second
		hi = (nodeSyncHoldSec + nodeSyncHoldJitter) * time.Second
	)
	if hi >= 90*time.Second {
		t.Fatalf("longest hold %v does not leave room under the agent's 90s sync timeout", hi)
	}

	seen := map[time.Duration]bool{}
	for range 500 {
		d := nodeSyncHold()
		if d < lo || d > hi {
			t.Fatalf("hold %v outside [%v, %v]", d, lo, hi)
		}
		seen[d] = true
	}
	if len(seen) < 10 {
		t.Errorf("only %d distinct holds in 500 draws — still a metronome", len(seen))
	}
}
