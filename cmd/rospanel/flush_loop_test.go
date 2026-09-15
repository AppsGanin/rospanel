package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A flush asked for early runs without waiting for the tick, but early flushes stay
// at least the minimum gap apart however often they are asked for; the tick still
// flushes on its own, and the loop ends with its context.
func TestFlushLoopEarlyButSpaced(t *testing.T) {
	var flushes atomic.Int64
	var stamps []time.Time
	due := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	start := time.Now()
	go func() {
		flushLoop(ctx, time.Hour, 200*time.Millisecond, due, func() {
			stamps = append(stamps, time.Now())
			flushes.Add(1)
		})
		close(done)
	}()

	// Asked for continuously for ~700ms: flushes happen, but no closer than the gap.
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case due <- struct{}{}:
		default:
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	n := flushes.Load()
	if n < 2 || n > 5 {
		t.Fatalf("%d flushes in ~700ms of constant asking with a 200ms gap", n)
	}
	if stamps[0].Sub(start) < 190*time.Millisecond {
		t.Fatalf("the first early flush came %v after start, inside the gap", stamps[0].Sub(start))
	}
	for i := 1; i < len(stamps); i++ {
		if gap := stamps[i].Sub(stamps[i-1]); gap < 190*time.Millisecond {
			t.Fatalf("flushes %d and %d only %v apart", i-1, i, gap)
		}
	}

	// The tick flushes with nobody asking.
	var ticked atomic.Int64
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() {
		flushLoop(ctx2, 50*time.Millisecond, time.Second, make(chan struct{}), func() { ticked.Add(1) })
		close(done2)
	}()
	time.Sleep(180 * time.Millisecond)
	cancel2()
	<-done2
	if ticked.Load() < 2 {
		t.Fatalf("the tick flushed %d times in 180ms at 50ms", ticked.Load())
	}
}
