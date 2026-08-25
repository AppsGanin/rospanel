package logbuf

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLocation(t *testing.T) {
	loc := Location()
	if loc == nil {
		t.Fatal("Location() returned nil")
	}

	utc, _ := time.LoadLocation("UTC")
	SetLocation(utc)
	if got := Location(); got != utc {
		t.Errorf("Location() after SetLocation = %v; want UTC", got)
	}
}

func TestHubWriteAndTail(t *testing.T) {
	h := New()

	// Write empty or newline only
	n, err := h.Write([]byte("\n\n"))
	if err != nil || n != 2 {
		t.Errorf("h.Write empty = (%d, %v); want (2, nil)", n, err)
	}
	if len(h.Tail()) != 0 {
		t.Errorf("h.Tail() on empty write = %v; want empty", h.Tail())
	}

	// Write multiple lines
	lines := "line1\nline2\nline3\n"
	n, err = h.Write([]byte(lines))
	if err != nil || n != len(lines) {
		t.Errorf("h.Write = (%d, %v); want (%d, nil)", n, err, len(lines))
	}

	tail := h.Tail()
	if len(tail) != 3 || tail[0] != "line1" || tail[1] != "line2" || tail[2] != "line3" {
		t.Errorf("h.Tail() = %v; want [line1, line2, line3]", tail)
	}
}

func TestHubRingBufferLimit(t *testing.T) {
	h := New()

	// Write bufferSize + 50 lines
	for i := 0; i < bufferSize+50; i++ {
		_, _ = h.Write([]byte(fmt.Sprintf("entry-%d\n", i)))
	}

	tail := h.Tail()
	if len(tail) != bufferSize {
		t.Fatalf("h.Tail() len = %d; want capped at %d", len(tail), bufferSize)
	}

	// The oldest entries should have been evicted
	if tail[0] != "entry-50" {
		t.Errorf("oldest entry in ring = %q; want %q", tail[0], "entry-50")
	}
	if tail[bufferSize-1] != fmt.Sprintf("entry-%d", bufferSize+49) {
		t.Errorf("newest entry in ring = %q; want %q", tail[bufferSize-1], fmt.Sprintf("entry-%d", bufferSize+49))
	}
}

func TestHubSubscribeAndUnsubscribe(t *testing.T) {
	h := New()

	ch, unsub := h.Subscribe()

	// Write a line
	_, _ = h.Write([]byte("live-message\n"))

	select {
	case msg := <-ch:
		if msg != "live-message" {
			t.Errorf("received message = %q; want %q", msg, "live-message")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscriber message")
	}

	// Unsubscribe
	unsub()

	// Channel should be closed
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel still open after unsub()")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}

	// Subsequent unsubs should not panic
	unsub()
}

func TestHubSlowSubscriberNonBlocking(t *testing.T) {
	h := New()

	ch, unsub := h.Subscribe()
	defer unsub()

	// Fill subscriber channel capacity (256) + write more without reading
	for i := 0; i < 300; i++ {
		_, err := h.Write([]byte(fmt.Sprintf("msg-%d\n", i)))
		if err != nil {
			t.Fatalf("Write failed on slow subscriber: %v", err)
		}
	}

	// First 256 messages should be in ch
	for i := 0; i < 256; i++ {
		<-ch
	}

	// Tail should still have all 300 entries
	if len(h.Tail()) != 300 {
		t.Errorf("Tail len = %d; want 300", len(h.Tail()))
	}
}

func TestHubConcurrentAccess(t *testing.T) {
	h := New()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = h.Write([]byte(fmt.Sprintf("worker-%d-msg-%d\n", workerID, j)))
				_ = h.Tail()
			}
		}(i)
	}

	wg.Wait()
	if len(h.Tail()) != 500 {
		t.Errorf("h.Tail() len = %d; want 500", len(h.Tail()))
	}
}
