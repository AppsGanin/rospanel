package core

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/store"
)

// Stub bodies must carry telegramSDKMarker — fetchTelegramSDK rejects anything that
// doesn't look like the real wrapper, so a marker-less fixture would be discarded and
// every test would read as a fetch failure.
const (
	fakeSDK      = "// WebView\nwindow.Telegram.WebApp = {platform:'test'};"
	fakeSDKFresh = "// WebView fresh\nwindow.Telegram.WebApp = {platform:'test2'};"
)

// tgTestManager builds a Manager with a real (empty) store. The SDK cache itself
// needs no data, but the fetch reads the Telegram proxy setting, so a nil store
// would panic — and guarding for one in production code would be inventing a state
// the panel never has.
func tgTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tgsdk.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Manager{store: st}
}

// stubTelegramSDKFetch swaps the upstream GET for the duration of a test and
// reports how many times it was called.
func stubTelegramSDKFetch(t *testing.T, fn func(ctx context.Context) ([]byte, error)) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	prev := telegramSDKFetch
	telegramSDKFetch = func(ctx context.Context, _ string) ([]byte, error) {
		calls.Add(1)
		return fn(ctx)
	}
	t.Cleanup(func() { telegramSDKFetch = prev })
	return &calls
}

// waitTelegramSDKIdle blocks until no fetch is in flight. Background refreshes
// outlive the call that spawned them, so a test must settle them before its cleanup
// restores telegramSDKFetch — otherwise the in-flight goroutine reads the var as it
// is reassigned (a test-only race; production never reassigns it).
func waitTelegramSDKIdle(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.tgSDKMu.Lock()
		idle := m.tgSDKWait == nil
		m.tgSDKMu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a telegram SDK fetch is still in flight after 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitTelegramSDKCalls waits until the stub has been entered `want` times and the
// resulting fetch has finished. Needed because a background refresh is spawned with
// `go`: waiting only for "no fetch in flight" can't tell "not scheduled yet" from
// "already done", so the test would race ahead and observe zero calls.
func waitTelegramSDKCalls(t *testing.T, m *Manager, calls *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("upstream called %d times after 5s, want %d", calls.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitTelegramSDKIdle(t, m)
}

// A cold cache must fetch INLINE and hand the real body to the very first caller —
// that's the whole point of the cold path (an empty file would silently disable the
// Mini App bridge for whoever loads the page first).
func TestTelegramSDKColdFetchesInline(t *testing.T) {
	calls := stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		return []byte(fakeSDK), nil
	})
	m := tgTestManager(t)

	body, ok := m.TelegramWebAppSDK()
	if !ok || string(body) != fakeSDK {
		t.Fatalf("cold read: got (%q, %v), want the fetched body", body, ok)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream called %d times, want 1", got)
	}

	// Second read is served from cache — no new fetch.
	if body, ok = m.TelegramWebAppSDK(); !ok || string(body) != fakeSDK {
		t.Fatalf("warm read: got (%q, %v)", body, ok)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("warm read refetched (%d calls), want still 1", got)
	}
}

// An unreachable telegram.org must degrade to "empty body" AND arm the cooldown, so
// only the first caller pays the timeout. Without this, every page load would block
// for the full budget whenever upstream is down.
func TestTelegramSDKUnreachableArmsCooldown(t *testing.T) {
	calls := stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	m := tgTestManager(t)

	if body, ok := m.TelegramWebAppSDK(); ok || body != nil {
		t.Fatalf("failed fetch: got (%q, %v), want (nil, false)", body, ok)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream called %d times, want 1", got)
	}

	// Within the cooldown: return immediately, do NOT retry upstream.
	for i := 0; i < 5; i++ {
		if _, ok := m.TelegramWebAppSDK(); ok {
			t.Fatal("expected ok=false during cooldown")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cooldown was not honoured: %d upstream calls, want 1", got)
	}

	// Once the cooldown lapses, it tries again.
	m.tgSDKMu.Lock()
	m.tgSDKFailAt = time.Now().Add(-telegramSDKRetryGap - time.Second)
	m.tgSDKMu.Unlock()
	if _, ok := m.TelegramWebAppSDK(); ok {
		t.Fatal("still failing upstream, expected ok=false")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("after cooldown: %d upstream calls, want 2", got)
	}
}

// Concurrent cold readers must collapse onto ONE upstream fetch and all receive the
// body — a thundering herd on a cold cache would otherwise hammer telegram.org once
// per page load.
func TestTelegramSDKConcurrentColdSingleflight(t *testing.T) {
	release := make(chan struct{})
	calls := stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		<-release // hold the fetch open so every goroutine piles up behind it
		return []byte(fakeSDK), nil
	})
	m := tgTestManager(t)

	const readers = 12
	var wg sync.WaitGroup
	got := make([]bool, readers)
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, ok := m.TelegramWebAppSDK()
			got[i] = ok && string(body) == fakeSDK
		}()
	}
	time.Sleep(50 * time.Millisecond) // let them all reach the cold path
	close(release)
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Fatalf("upstream called %d times, want exactly 1 (singleflight)", n)
	}
	for i, okd := range got {
		if !okd {
			t.Errorf("reader %d did not get the body", i)
		}
	}
}

// A stale copy plus a failing upstream must NOT become a retry loop. A failed fetch
// never advances tgSDKAt, so the copy stays stale and every request re-triggers the
// refresh — without the cooldown guard the panel dials a blocked telegram.org
// back-to-back for as long as the page sees traffic.
func TestTelegramSDKStaleFailureDoesNotLoop(t *testing.T) {
	calls := stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		return nil, errors.New("dial tcp: i/o timeout")
	})
	m := tgTestManager(t)
	m.tgSDKBody = []byte("old")
	m.tgSDKAt = time.Now().Add(-telegramSDKTTL - time.Minute) // stale

	// First read kicks off a refresh, which fails and arms the cooldown.
	if body, ok := m.TelegramWebAppSDK(); !ok || string(body) != "old" {
		t.Fatalf("stale read: got (%q, %v), want the old body", body, ok)
	}
	waitTelegramSDKCalls(t, m, calls, 1)

	// Hammer it: still stale, still failing — the cooldown must absorb every one.
	for range 50 {
		if body, ok := m.TelegramWebAppSDK(); !ok || string(body) != "old" {
			t.Fatalf("stale read regressed: got (%q, %v)", body, ok)
		}
	}
	waitTelegramSDKIdle(t, m)
	if got := calls.Load(); got != 1 {
		t.Fatalf("stale+failing upstream looped: %d upstream calls after 51 reads, want 1", got)
	}
}

// A 200 that isn't actually the SDK (a transparent-proxy block page, or a body
// truncated at the size cap — which returns no error) must not be cached: it would
// be served as JS to every user for a full TTL.
func TestTelegramSDKRejectsNonSDKBody(t *testing.T) {
	stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		return []byte("<html><body>Access denied</body></html>"), nil
	})
	m := tgTestManager(t)

	if body, ok := m.TelegramWebAppSDK(); ok || body != nil {
		t.Fatalf("garbage body was accepted: got (%q, %v), want (nil, false)", body, ok)
	}
	m.tgSDKMu.Lock()
	cached, failed := m.tgSDKBody, !m.tgSDKFailAt.IsZero()
	m.tgSDKMu.Unlock()
	if cached != nil {
		t.Errorf("garbage body got cached: %q", cached)
	}
	if !failed {
		t.Error("a rejected body must arm the failure cooldown like any other failure")
	}
}

// A stale copy is served immediately (never blocking the page) while the refresh
// happens behind it.
func TestTelegramSDKStaleServedImmediately(t *testing.T) {
	block, started := make(chan struct{}), make(chan struct{})
	var once sync.Once
	stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		once.Do(func() { close(started) })
		<-block
		return []byte(fakeSDKFresh), nil
	})
	m := tgTestManager(t)
	m.tgSDKBody = []byte("old")
	m.tgSDKAt = time.Now().Add(-telegramSDKTTL - time.Minute) // stale

	start := time.Now()
	body, ok := m.TelegramWebAppSDK()
	elapsed := time.Since(start)
	if !ok || string(body) != "old" {
		t.Fatalf("stale read: got (%q, %v), want the old body", body, ok)
	}
	if elapsed > time.Second {
		t.Fatalf("stale read blocked for %v — it must not wait on the refresh", elapsed)
	}

	// Wait for the refresh to actually BEGIN before releasing it: the goroutine may
	// not have been scheduled yet, and "no fetch in flight" can't distinguish
	// "not started" from "finished".
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("background refresh never started")
	}
	close(block)
	waitTelegramSDKIdle(t, m) // let it land before cleanup restores the stub

	// ...and the refresh it kicked off replaced the stale copy.
	if body, ok := m.TelegramWebAppSDK(); !ok || string(body) != fakeSDKFresh {
		t.Fatalf("after refresh: got (%q, %v), want the fresh body", body, ok)
	}
}
