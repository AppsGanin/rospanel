package xray

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// An api call that cannot reach Xray is asked again for seconds; a stop must not wait
// that out.
func TestAbortEndsAPIRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	bin := filepath.Join(t.TempDir(), "xray")
	script := "#!/bin/sh\necho 'failed to dial 127.0.0.1:10085' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSupervisor(bin, filepath.Join(t.TempDir(), "config.json"), t.TempDir())
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := s.runXrayAPI(5*time.Second, "api", "rmu", "--server=127.0.0.1:1", "-tag=x", "u1")
		done <- err
	}()
	time.Sleep(300 * time.Millisecond) // inside the first wait
	s.Abort()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an aborted call reported success")
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("abort took %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the retries went on after Abort")
	}
	if !s.aborted() {
		t.Fatal("not marked aborted")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if !closed {
		t.Fatal("an aborted supervisor would start Xray again")
	}
}

// A Supervisor built without NewSupervisor never aborts and never blocks on it.
func TestBareSupervisorAbortSafe(t *testing.T) {
	s := &Supervisor{}
	if s.aborted() {
		t.Fatal("bare supervisor reads as aborted")
	}
	if !s.pause(time.Millisecond) {
		t.Fatal("pause on a bare supervisor gave up")
	}
	s.Abort() // no channel: must not panic
}
