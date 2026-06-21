package xray

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeXray writes a shell script that mimics the xray CLI we drive: `run -test`
// exits 0 (config validation), `run -c <cfg>` blocks (a running daemon).
func fakeXray(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = run ] && [ \"$2\" = -test ]; then exit 0; fi\n" +
		"if [ \"$1\" = run ]; then exec sleep 60; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func newTestSup(t *testing.T) *Supervisor {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.json")
	return NewSupervisor(fakeXray(t), cfg, "")
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (s *Supervisor) curPID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil || s.cur.cmd.Process == nil {
		return 0
	}
	return s.cur.cmd.Process.Pid
}

// TestAutoRestart: an unexpected exit (simulated crash) is auto-restarted.
func TestAutoRestart(t *testing.T) {
	s := newTestSup(t)
	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	waitFor(t, "initial start", s.Running)
	old := s.curPID()
	if old == 0 {
		t.Fatal("no pid after start")
	}

	// Simulate a crash: kill the OS process directly (not via Stop), so the
	// monitor should treat it as unexpected and restart.
	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	_ = p.cmd.Process.Kill()

	waitFor(t, "auto-restart with new pid", func() bool {
		return s.Running() && s.curPID() != 0 && s.curPID() != old
	})
	s.Stop()
}

// TestStopNoRestart: an intentional Stop must not be auto-restarted, and
// Running() must report false afterwards.
func TestStopNoRestart(t *testing.T) {
	s := newTestSup(t)
	if err := s.Apply(&Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	waitFor(t, "start", s.Running)
	s.Stop()
	if s.Running() {
		t.Fatal("still running right after Stop")
	}
	// Stays down (no resurrection by a stray supervise goroutine).
	time.Sleep(1500 * time.Millisecond)
	if s.Running() {
		t.Fatal("resurrected after Stop")
	}
}

// TestConcurrentApply: many concurrent Applies must serialize cleanly (run with
// -race) and leave exactly one process running.
func TestConcurrentApply(t *testing.T) {
	s := newTestSup(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Apply(&Config{}); err != nil {
				t.Errorf("apply: %v", err)
			}
		}()
	}
	wg.Wait()
	waitFor(t, "running after applies", s.Running)
	pid := s.curPID()
	time.Sleep(200 * time.Millisecond)
	if s.curPID() != pid {
		t.Fatalf("process churned after applies settled: %d -> %d", pid, s.curPID())
	}
	s.Stop()
}
