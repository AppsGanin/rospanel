package nodeagent

import (
	"fmt"
	"io"
	"testing"
	"time"
)

func TestBenignPollCut(t *testing.T) {
	benign := []error{
		io.EOF,
		io.ErrUnexpectedEOF,
		fmt.Errorf("Post \"https://x/sync\": %w", io.ErrUnexpectedEOF),
		fmt.Errorf("http2: server sent GOAWAY and closed the connection; LastStreamID=41"),
		fmt.Errorf("read tcp: connection reset by peer"),
	}
	for _, e := range benign {
		if !benignPollCut(e) {
			t.Errorf("benignPollCut(%v) = false, want true (poll cut → re-poll, not back off)", e)
		}
	}
	hard := []error{
		nil,
		fmt.Errorf("dial tcp 1.2.3.4:443: i/o timeout"),
		fmt.Errorf("dial tcp: lookup panel: no such host"),
		fmt.Errorf("connection refused"),
		fmt.Errorf("panel returned HTTP 404"),
	}
	for _, e := range hard {
		if benignPollCut(e) {
			t.Errorf("benignPollCut(%v) = true, want false (unreachable → back off)", e)
		}
	}
}

func TestRecentSyncFailsWindow(t *testing.T) {
	a := &Agent{}
	if a.recentSyncFails() != 0 {
		t.Fatal("fresh agent should report 0 sync fails")
	}
	for range 5 {
		a.noteSyncFail()
	}
	if got := a.recentSyncFails(); got != 5 {
		t.Fatalf("recentSyncFails = %d, want 5", got)
	}
	// An entry aged out of the window must not be counted.
	cutoff := int64(syncFailWindow.Seconds())
	a.syncFailMu.Lock()
	a.syncFailAt = append(a.syncFailAt, 1) // unix=1, far outside the window
	a.syncFailMu.Unlock()
	if got := a.recentSyncFails(); got != 5 {
		t.Fatalf("recentSyncFails = %d, want 5 (the ancient entry must not count); cutoff=%d", got, cutoff)
	}
}

// A panel restart makes every poll in flight fail, and an operator who restarts a
// few times in a row can push a healthy node past the panel's "unstable" threshold
// (six in the hour) — leaving it flagged for the rest of the hour for the panel's
// own downtime. One poll the panel held to the end and answered proves the transport
// carries a long request right now, so the failures before it are history.
func TestClearSyncFailsForgetsTheWindow(t *testing.T) {
	a := &Agent{}
	for range 7 {
		a.noteSyncFail()
	}
	if got := a.recentSyncFails(); got != 7 {
		t.Fatalf("recentSyncFails = %d, want 7 before the held poll", got)
	}

	a.clearSyncFails()
	if got := a.recentSyncFails(); got != 0 {
		t.Fatalf("recentSyncFails = %d after a held poll, want 0", got)
	}

	// Clearing reuses the backing array, so the window has to rebuild from scratch
	// rather than resurrect what was in it.
	a.noteSyncFail()
	if got := a.recentSyncFails(); got != 1 {
		t.Fatalf("recentSyncFails = %d after one new failure, want 1", got)
	}
}

// The rule only holds because a poll cannot reach minHeldPoll without the panel
// actually holding it: the panel's no-change hold is 45s jittered by ±15, so the
// shortest hold it can pick is 30s. A threshold at or above that would let an
// ordinary immediate answer — a config push, which proves nothing about long
// requests — wipe the window.
func TestMinHeldPollSitsBelowTheShortestHold(t *testing.T) {
	const shortestPanelHold = 30 * time.Second // internal/server: 45s nominal, ±15s jitter
	if minHeldPoll >= shortestPanelHold {
		t.Fatalf("minHeldPoll = %v, must stay below the panel's shortest hold (%v)",
			minHeldPoll, shortestPanelHold)
	}
}
