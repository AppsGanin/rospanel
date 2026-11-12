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
	syncStallLimit = 15 * time.Minute
	// syncSlowLimit is when a silent loop gets its goroutines written down without a
	// restart. The panel calls a node offline after two minutes, and an operator who
	// restarts it by hand before syncStallLimit would otherwise leave nothing behind.
	syncSlowLimit   = 3 * time.Minute
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

// watchSyncStall calls onStall when the sync loop has gone limit without a beat, once
// per silence: a beat after it arms it again.
func (a *Agent) watchSyncStall(ctx context.Context, limit, every time.Duration, onStall func(silent time.Duration)) {
	t := time.NewTicker(every)
	defer t.Stop()
	var fired int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		beat := a.syncBeatAt.Load()
		if silent := time.Since(time.Unix(0, beat)); silent > limit && beat != fired {
			fired = beat
			onStall(silent)
		}
	}
}

// noteSlowSync writes the goroutines down while the loop is still silent and leaves
// the process running.
func (a *Agent) noteSlowSync(silent time.Duration) {
	path, loop := a.dumpStacks()
	slog.Warn("node: the sync loop has not come round — goroutines written down",
		"silent", silent.Round(time.Second), "stacks", path, "sync_loop", loop)
}

// exitOnStall ends the process so systemd starts a fresh one: whatever holds the
// loop holds it for good, and only a new process syncs again. The goroutines go to
// the log and to a file first — they are the only account of what it was stuck on.
func (a *Agent) exitOnStall(silent time.Duration) {
	path, loop := a.dumpStacks()
	slog.Error("node: the sync loop is stuck — restarting the agent",
		"silent", silent.Round(time.Second), "stacks", path, "sync_loop", loop)
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
		path, loop := a.dumpStacks()
		slog.Error("node: the stop did not finish — exiting anyway", "after", deadline, "stacks", path, "sync_loop", loop)
		os.Exit(1)
	}
}

// dumpStacks writes every goroutine to stacksFile, each with what it waits on and for
// how long. It returns the file ("" when it could not be written) and the sync loop's
// own stack, which is what the log line carries: with every connection a goroutine or
// two, the whole dump is too much for the journal.
func (a *Agent) dumpStacks() (path, syncLoop string) {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
	syncLoop = goroutineWith(buf.Bytes(), "nodeagent.(*Agent).syncLoop(")
	path = stacksFile(a.dataDir)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		path = ""
	}
	return path, syncLoop
}

// goroutineWith is the goroutine of a debug=2 dump whose stack calls fn, or "".
func goroutineWith(dump []byte, fn string) string {
	for _, g := range bytes.Split(dump, []byte("\n\n")) {
		if bytes.Contains(g, []byte(fn)) {
			return string(bytes.TrimSpace(g))
		}
	}
	return ""
}
