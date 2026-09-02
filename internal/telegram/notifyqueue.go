package telegram

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// A bot's outgoing notifications go through a queue rather than the goroutine that
// raised them.
//
// The panel calls its notifiers from wherever the event happened: the traffic poll
// (once a minute, holding the users it just accounted), a payment webhook, the
// abuse flush, a sign-in. Sending inside those calls means one Telegram round trip
// per message on that path — and Telegram is a network the panel does not control.
// A blocked bot, a revoked token behind a slow proxy or a chat that has gone away
// each cost seconds, and the poll that was only meant to count bytes waits for
// them; twenty users crossing a limit in the same minute waited twenty times.
//
// So: a bounded queue and a few workers. The caller hands over a closure and
// returns. Bounded because an unbounded one turns a stuck bot into unbounded
// memory; when it is full the message is DROPPED and said so, since a notification
// that arrives an hour late is worse than one that never came and left a log line
// where the operator looks.

// notifyQueueSize is how many messages may wait. Generous next to any real burst
// (a fleet-wide expiry sweep is tens), small enough that a stuck bot cannot grow
// the panel's memory.
const notifyQueueSize = 256

// notifyQueue delivers bot messages off the caller's goroutine.
type notifyQueue struct {
	name string
	ch   chan func(context.Context)

	dropped  atomic.Int64
	logMu    sync.Mutex
	lastWarn time.Time
}

func newNotifyQueue(name string) *notifyQueue {
	return &notifyQueue{name: name, ch: make(chan func(context.Context), notifyQueueSize)}
}

// submit hands a send to the workers. Never blocks: a full queue drops the message
// and reports it, at most once a minute so a stuck bot cannot flood the log either.
func (q *notifyQueue) submit(task func(context.Context)) {
	select {
	case q.ch <- task:
	default:
		n := q.dropped.Add(1)
		q.logMu.Lock()
		warn := time.Since(q.lastWarn) > time.Minute
		if warn {
			q.lastWarn = time.Now()
		}
		q.logMu.Unlock()
		if warn {
			log.Printf("telegram: %s notification queue full — dropped %d message(s); the bot is not keeping up", q.name, n)
		}
	}
}

// run works the queue until ctx ends. Several workers so one slow chat does not
// hold up the rest; the per-bot rate limiter still decides the actual pace.
func (q *notifyQueue) run(ctx context.Context, workers int) {
	for range workers {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case task := <-q.ch:
					task(ctx)
				}
			}
		}()
	}
}
