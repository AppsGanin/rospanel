package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// closeTestManager is a manager built the way the service builds one — through New,
// which is what actually starts the background loops. Most tests construct a bare
// &Manager{store: st} and start nothing; those cannot see this problem at all.
func closeTestManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "close.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	return New(st, sup, xray.Options{PanelDest: "127.0.0.1:8080"}, TLSPaths{}, dir), st
}

// Close has to actually stop everything, and the way to assert that is to let it
// block: every loop is registered on the WaitGroup, so a loop that does not watch the
// stop signal keeps Close from returning and this test times out.
//
// The bug this guards is quiet. A loop that outlives the store writes into a closed
// database — "sql: database is closed" from whichever loop ticked last, in a test that
// has already reported success, or in a shutdown that looked clean.
func TestCloseStopsEveryBackgroundLoop(t *testing.T) {
	m, st := closeTestManager(t)

	// Timed, not just awaited. Close is deliberately bounded (closeGrace) so a long
	// download cannot hang a shutdown — which means "Close returned" is true even when
	// a loop ignores the stop signal entirely. What distinguishes the two is the cost:
	// loops stop in microseconds, a loop that has to be given up on costs the whole
	// grace period. So this asserts promptness, which is the property that actually
	// says every loop is watching.
	returned := make(chan struct{})
	start := time.Now()
	go func() {
		m.Close()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(closeGrace + 15*time.Second):
		t.Fatal("Close did not return at all")
	}
	if d := time.Since(start); d >= closeGrace {
		t.Fatalf("Close took %s (the full grace period) — a background loop is not "+
			"watching the stop signal and had to be abandoned", d)
	}

	// The point of waiting: the store can now be closed with nothing left to write to it.
	if err := st.Close(); err != nil {
		t.Errorf("close store: %v", err)
	}
}

// Close is called from a shutdown path that may already have been triggered another
// way, so it must not panic on a second call — closing a closed channel does.
func TestCloseIsIdempotent(t *testing.T) {
	m, st := closeTestManager(t)
	defer st.Close()
	m.Close()
	m.Close()
	m.Close()
}

// A manager built directly, without New, has no loops and no stop channel. Close on
// one of those must be a no-op rather than a nil-channel panic — half the tests in
// this package construct managers that way.
func TestCloseOnAManagerThatNeverStarted(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bare.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	(&Manager{store: st}).Close()
}

// wait is what turns "sleep an hour, then work" into a loop that stops promptly. It
// has to answer false as soon as the manager is closing, or shutdown waits out the
// cadence — an hour, for the geo and iplist loops.
func TestWaitReturnsOnClose(t *testing.T) {
	m, st := closeTestManager(t)
	defer st.Close()

	// A wait far longer than any test can afford, interrupted by Close.
	result := make(chan bool, 1)
	go func() { result <- m.wait(time.Hour) }()
	time.Sleep(50 * time.Millisecond) // let the wait start
	m.Close()

	select {
	case carryOn := <-result:
		if carryOn {
			t.Error("wait said to carry on after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return after Close — a loop would sleep out its cadence")
	}
	// And a wait started after Close returns immediately rather than sleeping.
	start := time.Now()
	if m.wait(time.Hour) {
		t.Error("wait said to carry on after Close")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("wait after Close took %s, want it to return at once", d)
	}
}
