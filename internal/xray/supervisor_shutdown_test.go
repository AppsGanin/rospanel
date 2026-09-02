package xray

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestNoCrashAlertDuringShutdown reproduces a false alarm seen in production: on an
// ordinary `systemctl stop`, admins were paged with "Xray crashed" and
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

// A stop asks the process to go before it kills it: Xray closes its listeners and
// lets in-flight tunnels finish, which matters because every config save stops the
// process. A child that ignores the term is still killed, so the port is never held.
func TestStopSignalsBeforeKilling(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	ready := filepath.Join(dir, "ready")
	trap := filepath.Join(dir, "term")
	// `run -test` validates and exits; `run -c` installs a TERM handler, says it is
	// ready, and waits. The handler records the signal — a kill would leave no trace.
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = run ] && [ \"$2\" = -test ]; then exit 0; fi\n" +
		"if [ \"$1\" = run ]; then\n" +
		"  trap 'echo term > " + trap + "; exit 0' TERM\n" +
		"  echo ready > " + ready + "\n" +
		"  /bin/sleep 60 & wait\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSupervisor(bin, filepath.Join(dir, "config.json"), "")
	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// The handler has to be installed before the signal, or the test would be racing
	// the shell rather than testing the supervisor.
	waitFor(t, "the child's signal handler", func() bool {
		_, err := os.Stat(ready)
		return err == nil
	})

	s.Stop()
	if _, err := os.Stat(trap); err != nil {
		t.Fatalf("the process was killed without being asked to stop first: %v", err)
	}
	if s.Running() {
		t.Fatal("still running after Stop")
	}
}

// A child that ignores the term is still killed, so a config save never waits on a
// wedged process holding :443.
func TestStopKillsAChildThatIgnoresTheSignal(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	ready := filepath.Join(dir, "ready")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = run ] && [ \"$2\" = -test ]; then exit 0; fi\n" +
		"if [ \"$1\" = run ]; then\n" +
		"  trap '' TERM\n" +
		"  echo ready > " + ready + "\n" +
		"  /bin/sleep 60 & wait\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSupervisor(bin, filepath.Join(dir, "config.json"), "")
	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	waitFor(t, "the child", func() bool { _, err := os.Stat(ready); return err == nil })

	start := time.Now()
	s.Stop()
	if took := time.Since(start); took > stopGrace+3*time.Second {
		t.Fatalf("Stop waited %s on a child that ignores SIGTERM", took.Round(time.Millisecond))
	}
	if s.Running() {
		t.Fatal("still running after Stop")
	}
}
