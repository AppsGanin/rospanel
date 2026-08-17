package nodeagent

import (
	"fmt"
	"io"
	"testing"
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
