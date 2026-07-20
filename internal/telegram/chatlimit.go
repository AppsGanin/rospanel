package telegram

import (
	"sync"
	"time"
)

// chatLimiter caps how many updates one chat may drive per window.
//
// This is inbound protection, distinct from the outbound `limiter` in ratelimit.go
// (which paces what WE send so Telegram doesn't 429 us). Both public bots poll
// updates on a single goroutine and answer synchronously, and every reply blocks on
// the outbound limiter's one-second-per-chat slot — so without an inbound cap a
// single chat sending junk parks the shared loop for a second per message and
// stalls every other user's menu, registration and payment.
type chatLimiter struct {
	window time.Duration
	max    int

	mu   sync.Mutex
	seen map[int64]*rateWindow
}

// rateWindowGC caps the tracking map; stale windows are pruned past it so a long
// tail of one-off chats cannot grow it without bound.
const rateWindowGC = 1024

type rateWindow struct {
	start time.Time
	count int
}

func newChatLimiter(window time.Duration, max int) *chatLimiter {
	return &chatLimiter{window: window, max: max, seen: map[int64]*rateWindow{}}
}

// allow reports whether chatID may be served now. `first` is true only on the
// update that crosses the limit, so a caller can say "slow down" exactly once:
// answering every rejected message would make a flood produce more outbound traffic
// than it did inbound.
func (l *chatLimiter) allow(chatID int64, now time.Time) (allowed, first bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.seen) > rateWindowGC {
		for id, w := range l.seen {
			if now.Sub(w.start) >= l.window {
				delete(l.seen, id)
			}
		}
	}
	w := l.seen[chatID]
	if w == nil || now.Sub(w.start) >= l.window {
		l.seen[chatID] = &rateWindow{start: now, count: 1}
		return true, false
	}
	w.count++
	if w.count > l.max {
		return false, w.count == l.max+1
	}
	return true, false
}
