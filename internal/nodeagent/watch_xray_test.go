package nodeagent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A node that loses its Xray has to say so now, not whenever the panel next lets its
// poll go — which on a quiet node is up to the full 45-second hold away. The held
// request is the only channel it has, so speaking up means ending it.
//
// And a node with nothing to report must leave that request alone: the hold is what
// keeps a quiet fleet cheap.
func TestWatchServingReportsChangesAndOnlyChanges(t *testing.T) {
	var serving atomic.Bool
	serving.Store(true)
	var fires atomic.Int64
	var lastReported atomic.Bool

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchServing(ctx, serving.Load, func(s bool) {
		lastReported.Store(s)
		fires.Add(1)
	})

	// Steady: nothing to say.
	time.Sleep(3 * xrayWatchTick)
	if n := fires.Load(); n != 0 {
		t.Fatalf("a node whose Xray never changed cut its poll short %d time(s)", n)
	}

	// Xray dies — reported, and reported as down.
	serving.Store(false)
	waitFor(t, "the outage to be reported", func() bool { return fires.Load() == 1 })
	if lastReported.Load() {
		t.Error("the outage was reported as still serving")
	}

	// Staying down is not news either.
	time.Sleep(3 * xrayWatchTick)
	if n := fires.Load(); n != 1 {
		t.Errorf("a node that stayed down reported %d times, want 1", n)
	}
}

// interruptSync has to mark the cancellation as the agent's own. Without that the
// sync loop reads the ended request as the panel being unreachable and backs off —
// delaying the very report the interrupt existed to hurry.
func TestInterruptSyncMarksItsOwnCancellation(t *testing.T) {
	a := &Agent{}

	// Nothing in flight: nothing to cancel, and nothing to claim afterwards.
	a.interruptSync()
	if a.syncInterrupted.Load() {
		t.Error("an interrupt with no request in flight still claimed one")
	}

	var cancelled atomic.Bool
	a.syncMu.Lock()
	a.syncCancel = func() { cancelled.Store(true) }
	a.syncMu.Unlock()

	a.interruptSync()
	if !cancelled.Load() {
		t.Error("the in-flight request was not cancelled")
	}
	if !a.syncInterrupted.Load() {
		t.Error("the cancellation was not marked as ours — the loop would back off from it")
	}
}
