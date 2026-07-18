package telegram

import (
	"context"
	"testing"
	"time"
)

// resetLimiter clears the package-level send clocks so each test starts fresh.
func resetLimiter() {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.chat = map[int64]time.Time{}
	limiter.global = time.Time{}
}

// Messages to one chat must be spaced a second apart (Telegram's per-chat cap),
// while a different chat stays bound only by the global ceiling — a slow chat
// must not stall a broadcast.
func TestReservePacesPerChat(t *testing.T) {
	resetLimiter()
	for i, want := range []time.Duration{0, time.Second, 2 * time.Second} {
		got := reserve(42)
		if d := got - want; d < -50*time.Millisecond || d > 50*time.Millisecond {
			t.Fatalf("send %d to chat 42: wait %v, want ~%v", i, got, want)
		}
	}
	if got := reserve(43); got > 10*globalInterval {
		t.Fatalf("chat 43 waited %v behind chat 42, want ~%v", got, globalInterval)
	}
}

// A fan-out across many chats is paced by the global ceiling alone.
func TestReservePacesGlobally(t *testing.T) {
	resetLimiter()
	var last time.Duration
	for id := int64(0); id < 10; id++ {
		last = reserve(id)
	}
	if want := 9 * globalInterval; last < want-50*time.Millisecond || last > want+50*time.Millisecond {
		t.Fatalf("10th chat waited %v, want ~%v", last, want)
	}
}

// backOff must honour a 429's retry_after for the chat that earned it.
func TestBackOffDelaysChat(t *testing.T) {
	resetLimiter()
	backOff(7, 3*time.Second)
	if got := reserve(7); got < 2900*time.Millisecond {
		t.Fatalf("reserve after 3s back-off waited %v, want ~3s", got)
	}
	if got := reserve(8); got > 10*globalInterval {
		t.Fatalf("unrelated chat waited %v for another chat's penalty", got)
	}
}

func TestPollBackoffHonorsRetryAfter(t *testing.T) {
	err := &APIError{Code: 429, Description: "Too Many Requests: retry after 5", RetryAfter: 5 * time.Second}
	if got, want := pollBackoff(err), 6*time.Second; got != want {
		t.Fatalf("pollBackoff(429) = %v, want %v", got, want)
	}
	if got, want := pollBackoff(context.DeadlineExceeded), 15*time.Second; got != want {
		t.Fatalf("pollBackoff(non-429) = %v, want %v", got, want)
	}
}

// pruneChats must only drop chats whose slot has already passed.
func TestPruneChatsKeepsActive(t *testing.T) {
	resetLimiter()
	now := time.Now()
	limiter.mu.Lock()
	for id := int64(0); id < 300; id++ {
		limiter.chat[id] = now.Add(-time.Minute)
	}
	limiter.chat[999] = now.Add(time.Minute)
	pruneChats(now)
	got := len(limiter.chat)
	_, active := limiter.chat[999]
	limiter.mu.Unlock()
	if got != 1 || !active {
		t.Fatalf("after prune: %d entries, active kept=%v; want 1 entry, the active one", got, active)
	}
}
