package core

import (
	"testing"
	"time"
)

// The guard's cleanup loop used to be a bare goroutine over a ticker with no exit,
// started from the constructor — so it outlived the manager that built it, and
// Close had nothing to wait for. Closing done must end it.
func TestBruteGuardCleanupStopsWhenTheManagerDoes(t *testing.T) {
	t.Parallel()
	g := newBruteGuard()
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		g.cleanupLoop(done)
		close(exited)
	}()
	close(done)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanupLoop is still running after done closed — it would outlive its manager")
	}
}
