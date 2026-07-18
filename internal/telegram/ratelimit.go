package telegram

import (
	"context"
	"sync"
	"time"
)

// Telegram throttles outbound messages and answers a burst with HTTP 429
// ("Too Many Requests: retry after N") — which, unthrottled, shows up as
// silently dropped notifications during a broadcast to many chats. The
// documented ceilings are ~1 message per second to a single chat and ~30
// messages per second overall, so every send reserves a slot against both
// before it goes out.
const (
	chatInterval   = time.Second           // ≤ 1 message/second per chat
	globalInterval = 34 * time.Millisecond // ≈ 30 messages/second in total
)

// limiter hands out send slots. It is package-level on purpose: clients are
// constructed ad hoc (one per notification, per bot token), so a per-Client
// limiter would not throttle anything.
var limiter = struct {
	mu     sync.Mutex
	global time.Time           // earliest the next message may leave, any chat
	chat   map[int64]time.Time // chatID → earliest its next message may leave
}{chat: map[int64]time.Time{}}

// reserve claims the next send slot for chatID and returns how long the caller
// must wait before using it. Slots are handed out in call order, so concurrent
// senders queue instead of racing: each reservation pushes both the global and
// the per-chat clock forward for whoever comes next.
func reserve(chatID int64) time.Duration {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	at := now
	if limiter.global.After(at) {
		at = limiter.global
	}
	// The global clock advances by one slot from here — NOT from the (possibly far
	// later) time this chat's own 1s spacing forces. Otherwise a busy chat drags
	// the shared clock seconds into the future and stalls every other chat behind
	// it, which is exactly the broadcast case this limiter exists to keep flowing.
	limiter.global = at.Add(globalInterval)
	if t := limiter.chat[chatID]; t.After(at) {
		at = t
	}
	limiter.chat[chatID] = at.Add(chatInterval)
	pruneChats(now)
	return at.Sub(now)
}

// backOff pushes a chat's clock out by the retry_after Telegram just asked for,
// so the retry — and anything queued behind it — waits out the penalty too.
func backOff(chatID int64, d time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	until := time.Now().Add(d)
	if until.After(limiter.chat[chatID]) {
		limiter.chat[chatID] = until
	}
}

// pruneChats drops chats whose slot has already elapsed, so the map tracks only
// currently-throttled chats rather than every user the bot ever messaged. Cheap
// enough to amortise: it only sweeps once the map has actually grown.
func pruneChats(now time.Time) {
	if len(limiter.chat) < 256 {
		return
	}
	for id, t := range limiter.chat {
		if t.Before(now) {
			delete(limiter.chat, id)
		}
	}
}

// pollBackoff is how long a poll loop should idle after a failed getUpdates. A
// 429 names its own cool-off (two panels sharing one token, or a burst after a
// restart), and polling again before it elapses only earns another 429; anything
// else — bad token, gateway hiccup — gets the flat retry.
func pollBackoff(err error) time.Duration {
	if d, ok := RetryAfter(err); ok {
		return d + time.Second // a beat past the deadline, clocks differ
	}
	return 15 * time.Second
}

// waitSlot blocks until chatID's reserved slot comes up, or ctx ends.
func waitSlot(ctx context.Context, chatID int64) error {
	d := reserve(chatID)
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
