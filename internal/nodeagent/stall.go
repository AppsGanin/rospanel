package nodeagent

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"
)

const (
	// syncStallLimit is how long the sync loop may go without a beat outside a wait
	// it chose. One pass is a poll (syncTimeout) plus applying what it brought, a
	// self-update (5 min) at the longest; a loop silent for this long is stuck on
	// something that will not come back, and a node that does not sync is one the
	// panel shows offline while it keeps serving the users it had.
	syncStallLimit  = 15 * time.Minute
	stallCheckEvery = 30 * time.Second
	// stopDeadline bounds a stop: the last traffic sample (10 s) and Xray's SIGTERM
	// grace fit well inside it, and systemd's own 90 s ends in a SIGKILL that keeps
	// nothing and says nothing about why.
	stopDeadline = 45 * time.Second
	// stallTrafficWait bounds the last attempt to keep unsent traffic before an exit
	// over a stall: the lock it needs may be what is stuck.
	stallTrafficWait = 15 * time.Second
)

// stacksFile is where the goroutine dump of the last stall or stuck stop is kept.
func stacksFile(dataDir string) string { return filepath.Join(dataDir, "stuck-goroutines.txt") }

// syncBeat marks the sync loop alive. ahead is a wait it is about to take on purpose,
// so the beat lands where the wait ends.
func (a *Agent) syncBeat(ahead time.Duration) {
	a.syncBeatAt.Store(time.Now().Add(ahead).UnixNano())
}

// syncSleep is sleepCtx for the sync loop: the wait is the loop's own choice, not a
// stall.
func (a *Agent) syncSleep(ctx context.Context, d time.Duration) bool {
	a.syncBeat(d)
	return sleepCtx(ctx, d)
}

// watchSyncStall calls onStall once when the sync loop has gone limit without a beat.
func (a *Agent) watchSyncStall(ctx context.Context, limit, every time.Duration, onStall func(silent time.Duration)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if silent := time.Since(time.Unix(0, a.syncBeatAt.Load())); silent > limit {
			onStall(silent)
			return
		}
	}
}

// exitOnStall ends the process so systemd starts a fresh one: whatever holds the
// loop holds it for good, and only a new process syncs again. The goroutines go to
// the log and to a file first — they are the only account of what it was stuck on.
func (a *Agent) exitOnStall(silent time.Duration) {
	path := a.dumpStacks()
	slog.Error("node: the sync loop is stuck — restarting the agent", "silent", silent.Round(time.Second), "stacks", path)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.keepTrafficOnStop()
	}()
	select {
	case <-done:
	case <-time.After(stallTrafficWait):
	}
	os.Exit(1)
}

// exitIfStopHangs ends the process when a stop has not finished in time, after the
// same dump: stopped is closed once Run is done.
func (a *Agent) exitIfStopHangs(ctx context.Context, deadline time.Duration, stopped <-chan struct{}) {
	select {
	case <-stopped:
		return
	case <-ctx.Done():
	}
	select {
	case <-stopped:
	case <-time.After(deadline):
		path := a.dumpStacks()
		slog.Error("node: the stop did not finish — exiting anyway", "after", deadline, "stacks", path)
		os.Exit(1)
	}
}

// dumpStacks writes every goroutine, identical stacks folded together, to stderr
// (journald) and to stacksFile. Returns the file, or "" when it could not be written.
func (a *Agent) dumpStacks() string {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 1)
	_, _ = os.Stderr.Write(buf.Bytes())
	path := stacksFile(a.dataDir)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return ""
	}
	return path
}
