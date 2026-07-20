package xray

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestNoCrashAlertDuringShutdown reproduces a false alarm seen in production: on an
// ordinary `systemctl stop`, admins were paged with "Xray аварийно завершился" and
// then never got an all-clear.
//
// The cause is systemd's default KillMode=control-group, which SIGTERMs every
// process in the cgroup — so Xray dies on its own, before the panel's signal
// handler reaches Stop(). The exit then looks unexpected (p.stop is still false)
// and fires the crash callback; the recovery that would have cleared it never
// happens, because the panel exits before the scheduled restart.
//
// The supervisor must therefore treat ANY exit as intentional once it is closing,
// not only one it killed itself.
func TestNoCrashAlertDuringShutdown(t *testing.T) {
	s := newTestSup(t)
	var crashes, recoveries atomic.Int64
	s.SetOnCrash(func(error) { crashes.Add(1) })
	s.SetOnRecover(func() { recoveries.Add(1) })

	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	waitFor(t, "start", s.Running)

	// Mark the panel as shutting down, then let Xray die from a signal it received
	// independently — the cgroup-wide SIGTERM, reproduced here by killing the process
	// directly rather than through stopProc.
	s.mu.Lock()
	s.closed = true
	p := s.cur
	s.mu.Unlock()
	_ = p.cmd.Process.Kill()
	<-p.done

	// Give the monitor room to (wrongly) fire.
	time.Sleep(300 * time.Millisecond)

	if n := crashes.Load(); n != 0 {
		t.Fatalf("crash alert fired %d time(s) during shutdown — operators get paged "+
			"on every ordinary restart, and no all-clear ever follows", n)
	}
	if n := recoveries.Load(); n != 0 {
		t.Errorf("recovery fired %d time(s) during shutdown", n)
	}

	// The panel's handler reaches Stop() shortly after; it must cope with a child
	// that already died on its own, and must still not raise an alert.
	s.Stop()
	if s.Running() {
		t.Error("still running after Stop")
	}
	if n := crashes.Load(); n != 0 {
		t.Fatalf("crash alert fired %d time(s) across the whole shutdown", n)
	}
}

// TestCrashAlertStillFiresWhenRunning guards the other side: the shutdown check
// must not swallow a real crash while the panel is up and serving.
func TestCrashAlertStillFiresWhenRunning(t *testing.T) {
	s := newTestSup(t)
	var crashes, recoveries atomic.Int64
	s.SetOnCrash(func(error) { crashes.Add(1) })
	s.SetOnRecover(func() { recoveries.Add(1) })

	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	waitFor(t, "start", s.Running)
	old := s.curPID()

	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	_ = p.cmd.Process.Kill()

	waitFor(t, "auto-restart", func() bool {
		return s.Running() && s.curPID() != 0 && s.curPID() != old
	})
	waitFor(t, "crash alert", func() bool { return crashes.Load() > 0 })
	waitFor(t, "recovery alert", func() bool { return recoveries.Load() > 0 })
	s.Stop()
}
