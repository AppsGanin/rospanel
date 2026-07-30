package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf collects log output. A plain bytes.Buffer would race: a background
// refresh logs from its own goroutine while the test reads.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureLogs redirects the process-wide slog default (which logInfo/logWarn write
// through) into a buffer for the duration of one test.
func captureLogs(t *testing.T) *syncBuf {
	t.Helper()
	out := &syncBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return out
}

// An unreachable telegram.org must say WHY in the log. Without this the operator
// sees only an empty /tg.js and a subscription page whose "open in app" buttons do
// nothing — the failure that took a user's DevTools session to diagnose (#43).
func TestTelegramSDKFailureIsLogged(t *testing.T) {
	logs := captureLogs(t)
	stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		return nil, errors.New("dial tcp 149.154.167.99:443: i/o timeout")
	})
	m := &Manager{}

	if _, ok := m.TelegramWebAppSDK(); ok {
		t.Fatal("expected the stubbed failure to report ok=false")
	}
	got := logs.String()
	if !strings.Contains(got, "i/o timeout") {
		t.Errorf("log does not carry the upstream error; got %q", got)
	}
	if !strings.Contains(got, telegramSDKURL) {
		t.Errorf("log does not name the URL that failed; got %q", got)
	}
	if !strings.Contains(got, "level=WARN") {
		t.Errorf("a broken Mini App bridge must log at WARN; got %q", got)
	}
}

// A 200 carrying something that isn't the wrapper (a transparent-proxy block page,
// or a body truncated at telegramSDKMaxBytes) is discarded by the marker check. That
// must be distinguishable in the log from "could not connect" — the two point the
// operator at completely different causes.
func TestTelegramSDKBadBodyLogsDistinctly(t *testing.T) {
	logs := captureLogs(t)
	blockPage := []byte("<html>Доступ ограничен</html>")
	stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		return blockPage, nil
	})
	m := &Manager{}

	if _, ok := m.TelegramWebAppSDK(); ok {
		t.Fatal("a marker-less body must not be cached")
	}
	got := logs.String()
	if !strings.Contains(got, "unexpected body") {
		t.Errorf("log does not distinguish a bad body from a failed connection; got %q", got)
	}
	if want := fmt.Sprintf("bytes=%d", len(blockPage)); !strings.Contains(got, want) {
		t.Errorf("log does not report the body size that was rejected (want %q); got %q", want, got)
	}
}

// The retry cooldown is a minute, so a blocked telegram.org fails ~1440 times a day
// while the page sees traffic. Logging each one would flush the 1000-line dashboard
// ring and bury everything else, so repeats inside telegramSDKLogGap stay silent.
func TestTelegramSDKFailureLogIsRateLimited(t *testing.T) {
	logs := captureLogs(t)
	stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		return nil, errors.New("connection refused")
	})
	m := &Manager{}

	// Ten failures, each one past the RETRY cooldown so the fetch really re-runs.
	for i := 0; i < 10; i++ {
		if _, ok := m.TelegramWebAppSDK(); ok {
			t.Fatal("expected ok=false")
		}
		m.tgSDKMu.Lock()
		m.tgSDKFailAt = time.Time{}
		m.tgSDKMu.Unlock()
	}
	if n := strings.Count(logs.String(), "connection refused"); n != 1 {
		t.Fatalf("logged the same failure %d times, want 1 inside the log gap", n)
	}

	// Once the LOG gap lapses, it complains again — a still-broken panel should not
	// go quiet forever.
	m.tgSDKMu.Lock()
	m.tgSDKLogAt = time.Now().Add(-telegramSDKLogGap - time.Second)
	m.tgSDKFailAt = time.Time{}
	m.tgSDKMu.Unlock()
	if _, ok := m.TelegramWebAppSDK(); ok {
		t.Fatal("expected ok=false")
	}
	if n := strings.Count(logs.String(), "connection refused"); n != 2 {
		t.Fatalf("logged %d times after the gap lapsed, want 2", n)
	}
}

// Recovery is worth one line too: the operator who saw the warning needs to know it
// cleared. It's only emitted when a failure was actually reported, so a healthy
// panel stays quiet.
func TestTelegramSDKRecoveryIsLogged(t *testing.T) {
	logs := captureLogs(t)
	var fail = true
	stubTelegramSDKFetch(t, func(context.Context) ([]byte, error) {
		if fail {
			return nil, errors.New("connection refused")
		}
		return []byte(fakeSDK), nil
	})
	m := &Manager{}

	if _, ok := m.TelegramWebAppSDK(); ok {
		t.Fatal("expected the first fetch to fail")
	}
	fail = false
	m.tgSDKMu.Lock()
	m.tgSDKFailAt = time.Time{} // let the next read retry immediately
	m.tgSDKMu.Unlock()
	if _, ok := m.TelegramWebAppSDK(); !ok {
		t.Fatal("expected the second fetch to land")
	}
	if !strings.Contains(logs.String(), "reachable again") {
		t.Errorf("recovery was not logged; got %q", logs.String())
	}
}
