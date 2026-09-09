package nodeapi

import "testing"

// The panel and the agent read these from here precisely so they cannot drift. The
// relationships below are what make the pair work; break one and the symptom is a
// node that reports itself healthy while quietly backing off, which no log line says
// out loud.
func TestSyncCadenceHoldsTogether(t *testing.T) {
	minHold := HoldSec - HoldJitter
	maxHold := HoldSec + HoldJitter

	if HoldJitter <= 0 || HoldJitter >= HoldSec {
		t.Fatalf("jitter %d makes no sense against a %ds hold", HoldJitter, HoldSec)
	}
	// A recycled hold must always count as benign: the threshold sits below the
	// SHORTEST hold, or the agent reads the panel's own timing as a failure.
	if MinHeldPollSec >= minHold {
		t.Errorf("MinHeldPollSec=%d is not below the shortest hold (%ds)", MinHeldPollSec, minHold)
	}
	if MinHeldPollSec <= 0 {
		t.Errorf("MinHeldPollSec=%d would call every cut benign", MinHeldPollSec)
	}
	// And the request must not time out while the panel is still legitimately holding
	// it, with room for the round trip on top.
	if SyncTimeoutSec <= maxHold {
		t.Errorf("SyncTimeoutSec=%d does not clear the longest hold (%ds)", SyncTimeoutSec, maxHold)
	}
	if SyncTimeoutSec < maxHold*2 {
		t.Errorf("SyncTimeoutSec=%d leaves no headroom over a %ds hold", SyncTimeoutSec, maxHold)
	}
}
