package telegram

import (
	"context"
	"sync"
	"testing"
	"time"
)

// The point of the queue is that the panel's poll loops never wait on Telegram: a
// submit returns whether or not anything is being delivered, and a queue that has
// filled up drops rather than blocks.
func TestNotifyQueueNeverBlocksTheCaller(t *testing.T) {
	q := newNotifyQueue("test")

	// No workers: every submit lands in the buffer, and the one past it is dropped.
	for range notifyQueueSize {
		q.submit(func(context.Context) {})
	}
	done := make(chan struct{})
	go func() {
		q.submit(func(context.Context) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("submit blocked on a full queue — the caller's loop would stall on a stuck bot")
	}
	if n := q.dropped.Load(); n != 1 {
		t.Fatalf("dropped %d, want 1", n)
	}
}

func TestNotifyQueueRunsEverySubmittedTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := newNotifyQueue("test")
	q.run(ctx, 3)

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for range n {
		q.submit(func(context.Context) { wg.Done() })
	}
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("not every queued message was delivered")
	}
	if got := q.dropped.Load(); got != 0 {
		t.Errorf("dropped %d of %d with workers running", got, n)
	}

	// A cancelled context stops the workers; nothing panics and submit still returns.
	cancel()
	time.Sleep(50 * time.Millisecond)
	q.submit(func(context.Context) { t.Error("a task ran after shutdown") })
	time.Sleep(50 * time.Millisecond)
}
