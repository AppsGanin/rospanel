package xray

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The rule that decides whether a config change gets reverted after Xray goes down.
//
// The case that matters most is the one with no recent Apply: WriteConfig moves
// config.json ahead of the running process on every user sync and skips validation,
// so an unloadable config can sit there for hours while Xray serves from memory, then
// take the tunnels down at the next restart — a certificate renewal, say. Before this,
// the rollback slept through exactly that: it required a recent Apply, which that path
// never records.
func TestShouldRollback(t *testing.T) {
	yes := func() bool { return true }
	no := func() bool { return false }
	for _, tc := range []struct {
		name                                             string
		quick, backup, first, recentApply, alreadyRolled bool
		unloadable                                       func() bool
		want                                             bool
	}{
		{"unloadable config, no recent apply", true, true, false, false, false, yes, true},
		{"unloadable config, already rolled back once", true, true, false, false, true, yes, false},
		{"crash right after an apply", true, true, true, true, false, no, true},
		{"loadable config, crash is something else", true, true, false, false, false, no, false},
		{"ran healthy first — its config is proven", false, true, false, false, false, yes, false},
		{"no backup to go back to", true, false, true, true, false, yes, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRollback(tc.quick, tc.backup, tc.first, tc.recentApply,
				tc.alreadyRolled, tc.unloadable)
			if got != tc.want {
				t.Errorf("shouldRollback = %v, want %v", got, tc.want)
			}
		})
	}
}

// Asking Xray whether a config loads costs seven seconds. It must not be asked when
// the cheap terms have already answered — least of all on a healthy-then-crashed run,
// where the whole point is to leave a proven config alone.
func TestShouldRollbackDoesNotAskWhenItNeedNot(t *testing.T) {
	for _, tc := range []struct {
		name                                             string
		quick, backup, first, recentApply, alreadyRolled bool
	}{
		{"ran healthy", false, true, false, false, false},
		{"no backup", true, false, false, false, false},
		{"crash right after an apply", true, true, true, true, false},
		{"already rolled back", true, true, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := false
			shouldRollback(tc.quick, tc.backup, tc.first, tc.recentApply, tc.alreadyRolled,
				func() bool { asked = true; return true })
			if asked {
				t.Error("ran Xray over the config when the answer was already settled")
			}
		})
	}
}

// currentConfigUnloadable gates a rollback, so "we could not tell" must never read as
// "it is broken": that would revert a good config over a missing binary.
func TestCurrentConfigUnloadableFailsSafe(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")

	if (NewSupervisor("", cfg, dir)).currentConfigUnloadable() {
		t.Error("no Xray binary read as an unloadable config")
	}

	if runtime.GOOS == "windows" {
		t.Skip("the fake binary below is a shell script")
	}
	// A stand-in for Xray: refuses any config carrying the marker, accepts the rest.
	bin := filepath.Join(dir, "fake-xray")
	script := "#!/bin/sh\ngrep -q BROKEN \"$6\" && { echo 'bad config' >&2; exit 1; }\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("fake xray: %v", err)
	}
	sup := NewSupervisor(bin, cfg, dir)

	if sup.currentConfigUnloadable() {
		t.Error("a config file that does not exist read as an unloadable one")
	}
	if err := os.WriteFile(cfg, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if sup.currentConfigUnloadable() {
		t.Error("a good config read as unloadable")
	}
	if err := os.WriteFile(cfg, []byte(`{"BROKEN":true}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !sup.currentConfigUnloadable() {
		t.Error("an unloadable config went unnoticed — the rollback will not fire")
	}
}

// The rollback copy has to be the last config that RAN, not the last one that was
// structurally applied. Apply was its only writer, so everything a user sync wrote
// afterwards was missing from it — and a rollback that reaches for an arbitrarily old
// copy restores an arbitrarily old user set with it.
func TestHealthyConfigBecomesTheRollbackCopy(t *testing.T) {
	defer swapPromoteAfter(t)()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	sup := NewSupervisor("", cfg, dir)

	proven := []byte(`{"inbounds":["ran fine"]}`)
	p := &proc{done: make(chan struct{}), started: time.Now(), cfg: proven}
	sup.mu.Lock()
	sup.cur = p
	sup.mu.Unlock()

	sup.promoteWhenHealthy(p)
	got, err := os.ReadFile(cfg + ".bak")
	if err != nil {
		t.Fatalf("nothing was promoted: %v", err)
	}
	if string(got) != string(proven) {
		t.Errorf("rollback copy = %q, want the config this run proved", got)
	}
}

// A config that dies straight away must never become the thing we roll back TO.
func TestACrashedRunIsNotPromoted(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	sup := NewSupervisor("", cfg, dir)

	p := &proc{done: make(chan struct{}), started: time.Now(), cfg: []byte(`{"bad":true}`)}
	close(p.done) // it is already gone
	sup.mu.Lock()
	sup.cur = p
	sup.mu.Unlock()

	sup.promoteWhenHealthy(p)
	if _, err := os.Stat(cfg + ".bak"); err == nil {
		t.Error("a config that crashed immediately was promoted to the rollback copy")
	}
}

// Superseded runs must not promote either: by the time the wait is over, the config
// they were running is not the one on disk.
func TestASupersededRunIsNotPromoted(t *testing.T) {
	defer swapPromoteAfter(t)()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	sup := NewSupervisor("", cfg, dir)

	old := &proc{done: make(chan struct{}), started: time.Now(), cfg: []byte(`{"old":true}`)}
	sup.mu.Lock()
	sup.cur = &proc{done: make(chan struct{})} // something else took over
	sup.mu.Unlock()

	sup.promoteWhenHealthy(old)
	if _, err := os.Stat(cfg + ".bak"); err == nil {
		t.Error("a run that had already been replaced still promoted its config")
	}
}

// swapPromoteAfter shortens the proving period for a test and restores it after.
func swapPromoteAfter(t *testing.T) func() {
	t.Helper()
	prev := promoteAfter
	promoteAfter = time.Millisecond
	return func() { promoteAfter = prev }
}
