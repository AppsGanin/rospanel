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
	path, _ := a.dumpStacks()
	if path == "" {
		t.Fatal("no file written")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// debug=2: one block per goroutine, with its state.
	if !strings.Contains(string(b), "[running]") || !strings.Contains(string(b), "TestDumpStacksWritesFile") {
		t.Fatalf("not a goroutine dump:\n%.300s", b)
	}
}

// One report per silence, and another once the loop beat and fell silent again.
func TestWatchSyncStallOncePerSilence(t *testing.T) {
	a := &Agent{}
	a.syncBeat(-time.Hour)
	fired := make(chan time.Duration, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watchSyncStall(ctx, 30*time.Millisecond, 5*time.Millisecond, func(s time.Duration) { fired <- s })
	<-fired
	time.Sleep(60 * time.Millisecond)
	if len(fired) != 0 {
		t.Fatal("the same silence was reported twice")
	}
	a.syncBeat(-time.Hour) // came round, then stuck again
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("a second silence was not reported")
	}
}

func TestGoroutineWith(t *testing.T) {
	dump := "goroutine 1 [running]:\nmain.main()\n\ngoroutine 7 [semacquire, 4 minutes]:\nsync.(*Mutex).Lock()\n" +
		"github.com/AppsGanin/rospanel/internal/nodeagent.(*Agent).syncLoop(0x1)\n\ngoroutine 9 [IO wait]:\nnet.read()\n"
	got := goroutineWith([]byte(dump), "nodeagent.(*Agent).syncLoop(")
	if !strings.HasPrefix(got, "goroutine 7 [semacquire, 4 minutes]") || strings.Contains(got, "goroutine 9") {
		t.Fatalf("got %q", got)
	}
	if goroutineWith([]byte(dump), "nope(") != "" {
		t.Fatal("found a goroutine that is not there")
	}
}
