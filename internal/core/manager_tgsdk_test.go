package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubTelegramSDKFetch swaps the upstream GET for the duration of a test and
// reports how many times it was called.
func stubTelegramSDKFetch(t *testing.T, fn func(ctx context.Context) ([]byte, error)) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	prev := telegramSDKFetch
	telegramSDKFetch = func(ctx context.Context) ([]byte, error) {
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

// A cold cache must fetch INLINE and hand the real body to the very first caller —
// that's the whole point of the cold path (an empty file would silently disable the
// Mini App bridge for whoever loads the page first).
func TestTelegramSDKColdFetchesInline(t *testing.T) {
	calls := stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		return []byte("// WebView"), nil
	})
	m := &Manager{}

	body, ok := m.TelegramWebAppSDK()
	if !ok || string(body) != "// WebView" {
		t.Fatalf("cold read: got (%q, %v), want the fetched body", body, ok)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream called %d times, want 1", got)
	}

	// Second read is served from cache — no new fetch.
	if body, ok = m.TelegramWebAppSDK(); !ok || string(body) != "// WebView" {
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
	m := &Manager{}

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
		return []byte("// WebView"), nil
	})
	m := &Manager{}

	const readers = 12
	var wg sync.WaitGroup
	got := make([]bool, readers)
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, ok := m.TelegramWebAppSDK()
			got[i] = ok && string(body) == "// WebView"
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

// A stale copy is served immediately (never blocking the page) while the refresh
// happens behind it.
func TestTelegramSDKStaleServedImmediately(t *testing.T) {
	block, started := make(chan struct{}), make(chan struct{})
	var once sync.Once
	stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		once.Do(func() { close(started) })
		<-block
		return []byte("fresh"), nil
	})
	m := &Manager{}
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
	if body, ok := m.TelegramWebAppSDK(); !ok || string(body) != "fresh" {
		t.Fatalf("after refresh: got (%q, %v), want the fresh body", body, ok)
	}
}
