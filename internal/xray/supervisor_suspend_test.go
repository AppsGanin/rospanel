package xray

import (
	"testing"
	"time"
)

// A revoked node must stop serving, and STAY stopped. The crash supervisor is the
// hole: if Xray happens to be in its restart backoff when the panel revokes the
// node, nothing about "we killed it on purpose" is true yet — the loop simply wakes
// up and starts Xray again, and a disabled server goes back to carrying users with
// credentials the panel has withdrawn.
//
// Stop() closed that path by latching the supervisor shut. Suspend() must close it
// too, without being permanent.
func TestSuspendStopsTheCrashSupervisor(t *testing.T) {
	s := newTestSup(t)
	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	waitFor(t, "start", s.Running)

	// Kill it the way a crash does — behind the supervisor's back, so the monitor
	// sees an unexpected exit and schedules a restart.
	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	_ = p.cmd.Process.Kill()
	<-p.done

	// Revoke while that restart is still pending in backoff.
	s.Suspend()

	// Well past the first backoff delay: nothing may have come back.
	time.Sleep(restartBackoff + 500*time.Millisecond)
	if s.Running() {
		t.Fatal("the crash supervisor restarted Xray on a suspended (revoked) node")
	}
	if s.Serving() {
		t.Error("a suspended node reports itself as serving")
	}

	// And the suspension is still only a pause: re-enabling brings it back.
	if err := s.Resume(); err != nil {
		t.Fatalf("resume after Suspend: %v", err)
	}
	waitFor(t, "resume after Suspend", s.Running)
}

// TestServingCoversRestartWindowButNotCrash pins the difference between the two
// "is Xray up" answers. Running() is the instant truth; Serving() is what a status
// report should use — it stays true across a deliberate bounce (so a sync landing
// in the sub-second gap doesn't paint the node down for a whole poll cycle), but
// goes false for a crash (which is a real outage the operator must see).
func TestServingCoversRestartWindowButNotCrash(t *testing.T) {
	s := newTestSup(t)
	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	waitFor(t, "start", s.Running)
	if !s.Serving() {
		t.Fatal("Serving() is false while Xray is up")
	}

	// The mid-restart window: the old process is gone, the new one not yet up, and
	// restart() is holding the bounce flag. Report "serving", not "down".
	s.mu.Lock()
	s.cur = nil
	s.restarting = true
	s.mu.Unlock()
	if s.Running() {
		t.Error("Running() is true with no process")
	}
	if !s.Serving() {
		t.Error("Serving() went false during a deliberate restart — a healthy bounce reads as an outage")
	}

	// A crash looks the same to Running() but leaves restarting false: Serving must
	// then report the outage rather than hide it.
	s.mu.Lock()
	s.restarting = false
	s.mu.Unlock()
	if s.Serving() {
		t.Error("Serving() stayed true after a crash — a real outage would be hidden")
	}
}

// TestSuspendCanBeUndone pins the difference between the two ways of stopping Xray.
//
// A node is revoked whenever an operator switches it off in the panel — and switched
// back on just as often. That path used Stop, which latches the supervisor closed:
// afterwards every Apply and Restart returned success and started nothing. On a live
// node it looked like this — the agent kept syncing, the panel kept reporting the node
// online, each "перезапустить Xray" logged "restarted on operator request", and no
// process ever came back until someone restarted the agent by hand.
func TestSuspendCanBeUndone(t *testing.T) {
	s := newTestSup(t)
	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	waitFor(t, "start", s.Running)

	s.Suspend()
	if s.Running() {
		t.Fatal("still running after Suspend")
	}

	// Applying a config while switched off must write it and leave it written — a
	// revoked node accepting a push is not a reason to put it back on the air.
	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply while suspended: %v", err)
	}
	if s.Running() {
		t.Fatal("a config apply restarted Xray on a suspended node")
	}
	if s.Serving() {
		t.Error("a suspended node reported itself as serving while applying a config")
	}
	if err := s.Restart(); err != nil {
		t.Fatalf("restart while suspended: %v", err)
	}
	if s.Running() {
		t.Fatal("Restart lifted the suspension — only Resume may do that")
	}

	if err := s.Resume(); err != nil {
		t.Fatalf("resume after Suspend: %v", err)
	}
	waitFor(t, "resume after Suspend", s.Running)

	// And the terminal one stays terminal: Stop must keep refusing to come back, or
	// a shutting-down process could resurrect Xray on its way out.
	s.Stop()
	if err := s.Restart(); err != nil {
		t.Fatalf("restart after Stop returned an error: %v", err)
	}
	if s.Running() {
		t.Error("Stop is supposed to be final, but Restart brought Xray back")
	}
}
