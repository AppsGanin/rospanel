package telegram

import (
	"testing"
	"time"
)

// TestChatLimiterBoundsOneChat: the whole point is that a single chat cannot keep
// driving work. Both public bots poll on one goroutine and answer synchronously, so
// an unbounded chat stalls every other user.
func TestChatLimiterBoundsOneChat(t *testing.T) {
	l := newChatLimiter(time.Minute, 3)
	now := time.Now()

	for i := range 3 {
		if allowed, _ := l.allow(1, now); !allowed {
			t.Fatalf("update %d rejected while still inside the allowance", i+1)
		}
	}
	allowed, first := l.allow(1, now)
	if allowed {
		t.Fatal("limiter let a chat past its allowance")
	}
	if !first {
		t.Error("the update that crosses the limit must be flagged, so the caller can " +
			"say 'slow down' exactly once")
	}
	// Every later rejection is silent: replying to each would make a flood produce
	// more outbound traffic than it did inbound.
	if _, first := l.allow(1, now); first {
		t.Error("a second rejection was flagged as the first")
	}
}

// TestChatLimiterIsPerChat: one noisy chat must not lock anyone else out.
func TestChatLimiterIsPerChat(t *testing.T) {
	l := newChatLimiter(time.Minute, 2)
	now := time.Now()
	for range 5 {
		l.allow(1, now)
	}
	if allowed, _ := l.allow(1, now); allowed {
		t.Fatal("precondition: chat 1 should be over its limit")
	}
	if allowed, _ := l.allow(2, now); !allowed {
		t.Fatal("a different chat was punished for chat 1's flood")
	}
}

// TestChatLimiterWindowResets: the limit is per window, not a permanent ban.
func TestChatLimiterWindowResets(t *testing.T) {
	l := newChatLimiter(time.Minute, 2)
	now := time.Now()
	for range 3 {
		l.allow(1, now)
	}
	if allowed, _ := l.allow(1, now); allowed {
		t.Fatal("precondition: should be over the limit")
	}
	if allowed, _ := l.allow(1, now.Add(time.Minute+time.Second)); !allowed {
		t.Fatal("chat still blocked after its window elapsed")
	}
}

// TestChatLimiterPrunesStaleChats: the map must not grow one entry per chat that
// ever wrote to a long-lived process.
func TestChatLimiterPrunesStaleChats(t *testing.T) {
	l := newChatLimiter(time.Minute, 5)
	base := time.Now()
	for i := range rateWindowGC + 200 {
		l.allow(int64(i), base)
	}
	// A later update past the window triggers the sweep; the stale entries go.
	l.allow(-1, base.Add(2*time.Minute))

	l.mu.Lock()
	n := len(l.seen)
	l.mu.Unlock()
	if n > rateWindowGC {
		t.Fatalf("tracking map holds %d chats after the sweep, cap is %d", n, rateWindowGC)
	}
}

// TestInviteCodeBudgetIsTight pins the guessing budget specifically. The invite code
// is operator-chosen and often short; the comparison is constant-time, but that only
// stops a timing oracle — it does nothing about volume, and a hit mints a real
// account.
func TestInviteCodeBudgetIsTight(t *testing.T) {
	if maxCodesPerWindow > 10 {
		t.Errorf("invite-code allowance is %d per window — too generous to call a limit",
			maxCodesPerWindow)
	}
	if codeRateWindow < time.Minute {
		t.Errorf("invite-code window is %s — too short to slow a guesser down", codeRateWindow)
	}

	l := newChatLimiter(codeRateWindow, maxCodesPerWindow)
	now := time.Now()
	for i := range maxCodesPerWindow {
		if allowed, _ := l.allow(7, now); !allowed {
			t.Fatalf("attempt %d rejected inside the budget", i+1)
		}
	}
	if allowed, _ := l.allow(7, now); allowed {
		t.Fatal("guessing continued past the budget")
	}
	// And the budget is per chat, so it cannot be dodged from one account…
	if allowed, _ := l.allow(8, now); !allowed {
		t.Fatal("a different chat was blocked by chat 7's attempts")
	}
	// …but it does outlive a single window's worth of guesses.
	if allowed, _ := l.allow(7, now.Add(codeRateWindow-time.Second)); allowed {
		t.Fatal("budget refilled before the window elapsed")
	}
}
