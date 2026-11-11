package nodeagent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWatchSyncStallFiresOnSilence(t *testing.T) {
	a := &Agent{}
	a.syncBeat(0)
	fired := make(chan time.Duration, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watchSyncStall(ctx, 50*time.Millisecond, 10*time.Millisecond, func(s time.Duration) { fired <- s })
	select {
	case s := <-fired:
		if s < 50*time.Millisecond {
			t.Fatalf("fired after %v, before the limit", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a silent loop was not reported")
	}
}

func TestWatchSyncStallQuietWhileBeating(t *testing.T) {
	a := &Agent{}
	a.syncBeat(0)
	fired := make(chan time.Duration, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watchSyncStall(ctx, 80*time.Millisecond, 10*time.Millisecond, func(s time.Duration) { fired <- s })
	for i := 0; i < 20; i++ {
		a.syncBeat(0)
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case s := <-fired:
		t.Fatalf("a beating loop was reported stuck (silent %v)", s)
	default:
	}
}

// A wait the loop chose is not a stall, however long it is.
func TestSyncSleepCountsAsAlive(t *testing.T) {
	a := &Agent{}
	fired := make(chan time.Duration, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go a.watchSyncStall(ctx, 50*time.Millisecond, 10*time.Millisecond, func(s time.Duration) { fired <- s })
	sleepDone := make(chan struct{})
	go func() {
		defer close(sleepDone)
		a.syncSleep(ctx, 300*time.Millisecond)
	}()
	time.Sleep(200 * time.Millisecond)
	select {
	case s := <-fired:
		t.Fatalf("a chosen wait was reported as a stall (silent %v)", s)
	default:
	}
	cancel()
	<-sleepDone
}

func TestDumpStacksWritesFile(t *testing.T) {
	a := &Agent{dataDir: t.TempDir()}
	path := a.dumpStacks()
	if path == "" {
		t.Fatal("no file written")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "goroutine profile:") || !strings.Contains(string(b), "TestDumpStacksWritesFile") {
		t.Fatalf("not a goroutine dump:\n%.300s", b)
	}
}
