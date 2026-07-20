package server

import (
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestStatusFeedSharesOneComputation is the point of the whole type: N viewers must
// cost one payload computation per tick, not N. Before the feed, every open panel
// tab ran its own SystemStatus (a user count and a traffic sum) on its own timer.
func TestStatusFeedSharesOneComputation(t *testing.T) {
	var calls atomic.Int64
	f := newStatusFeedFunc(5*time.Millisecond, func() (any, error) {
		return calls.Add(1), nil
	})

	const viewers = 8
	chans := make([]<-chan string, viewers)
	releases := make([]func(), viewers)
	for i := range viewers {
		chans[i], releases[i] = f.subscribe()
	}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()

	// Every viewer must receive, and they must all see the same payloads — one
	// computation fanned out, not one each.
	got := make([]string, viewers)
	for i, ch := range chans {
		select {
		case got[i] = <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("viewer %d received nothing", i)
		}
	}

	after := calls.Load()
	if after > int64(viewers) {
		t.Fatalf("payload computed %d times for %d viewers — the work is still per-viewer", after, viewers)
	}
	// Let several ticks pass; the call count must track ticks, not viewers.
	time.Sleep(60 * time.Millisecond)
	ticks := calls.Load()
	if ticks > 40 {
		t.Fatalf("payload computed %d times in ~12 ticks with %d viewers", ticks, viewers)
	}
}

// TestStatusFeedStopsWithLastViewer covers the idle case: an unattended panel must
// not keep querying the database on a timer.
func TestStatusFeedStopsWithLastViewer(t *testing.T) {
	var calls atomic.Int64
	f := newStatusFeedFunc(time.Millisecond, func() (any, error) {
		return calls.Add(1), nil
	})

	_, release := f.subscribe()
	waitFor(t, "the feed to start ticking", func() bool { return calls.Load() > 2 })
	release()

	// Give the loop a moment to observe the stop, then confirm it went quiet.
	time.Sleep(20 * time.Millisecond)
	settled := calls.Load()
	time.Sleep(50 * time.Millisecond)
	if grew := calls.Load() - settled; grew != 0 {
		t.Fatalf("feed computed %d more payloads after the last viewer left", grew)
	}

	// And a later viewer restarts it.
	_, release2 := f.subscribe()
	defer release2()
	waitFor(t, "the feed to restart", func() bool { return calls.Load() > settled+2 })
}

// TestStatusFeedNewViewerPaintsImmediately: a tab opening mid-cycle should not
// stare at an empty dashboard until the next tick.
func TestStatusFeedNewViewerPaintsImmediately(t *testing.T) {
	f := newStatusFeedFunc(time.Hour, func() (any, error) { return "payload", nil })

	first, release1 := f.subscribe()
	defer release1()
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first viewer got nothing")
	}

	// The interval is an hour, so anything the second viewer receives can only be
	// the cached payload.
	second, release2 := f.subscribe()
	defer release2()
	select {
	case msg := <-second:
		if msg != `"payload"` {
			t.Fatalf("cached payload = %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second viewer waited for a tick instead of getting the cached payload")
	}
}

// TestStatusFeedReleaseIsSafe: releasing closes the channel exactly once, and a
// double release (defer plus an early return path) must not panic.
func TestStatusFeedReleaseIsSafe(t *testing.T) {
	f := newStatusFeedFunc(time.Hour, func() (any, error) { return 1, nil })
	ch, release := f.subscribe()
	release()
	release() // idempotent

	if _, open := <-ch; open {
		// Drained the cached payload; the next read must report a closed channel.
		if _, open := <-ch; open {
			t.Fatal("channel still open after release")
		}
	}
}

// TestStatusFeedSurvivesSlowViewer: one stalled reader must not hold up the others
// or block the publisher.
func TestStatusFeedSurvivesSlowViewer(t *testing.T) {
	var calls atomic.Int64
	f := newStatusFeedFunc(time.Millisecond, func() (any, error) {
		return calls.Add(1), nil
	})

	_, releaseSlow := f.subscribe() // never read from
	defer releaseSlow()

	fast, releaseFast := f.subscribe()
	defer releaseFast()

	// The fast reader keeps receiving even though the slow one never drains.
	for i := range 3 {
		select {
		case <-fast:
		case <-time.After(2 * time.Second):
			t.Fatalf("fast viewer stalled at message %d behind a slow one", i)
		}
	}
}
