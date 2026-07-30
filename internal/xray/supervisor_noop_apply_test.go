package xray

import (
	"testing"
	"time"
)

// applyCfg is a minimal config that fakeXray will happily "validate".
func applyCfg(loglevel string) *Config {
	return &Config{Log: &Log{Loglevel: loglevel}}
}

// Re-applying the SAME config must not restart Xray.
//
// A restart drops every live VPN connection, and the panel is served through Xray
// (:443 → the VLESS fallback → the loopback panel), so it also kills the admin's
// browser connection mid-request. Plenty of saves reach Apply without changing the
// generated config — renaming a lane, switching a client fingerprint, toggling TLS
// fragment — because those live in the subscription links, not in this file. Each one
// used to bounce every user off the VPN and hand the operator a "Failed to fetch" for
// a save that had actually succeeded.
func TestApplyIsANoOpForAnIdenticalConfig(t *testing.T) {
	s := newTestSup(t)
	t.Cleanup(s.Stop)

	if err := s.Apply(applyCfg("warning")); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	waitFor(t, "xray running", s.Running)
	first := s.StartedAt()
	if first == 0 {
		t.Fatal("no start time after the first apply")
	}
	applied := s.LastApply()
	if applied == 0 {
		t.Fatal("LastApply stayed zero after an apply")
	}

	// StartedAt/LastApply have second resolution, so let the clock move before the
	// second apply — otherwise "did not change" and "changed" look identical.
	time.Sleep(1100 * time.Millisecond)

	if err := s.Apply(applyCfg("warning")); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if got := s.StartedAt(); got != first {
		t.Errorf("identical config restarted Xray (start %d → %d) — every live VPN "+
			"connection would have been dropped for nothing", first, got)
	}
	// It still counts as applied: that is what tells the UI the save has landed, and
	// without it the "applying…" modal would spin out its whole timeout.
	if got := s.LastApply(); got <= applied {
		t.Errorf("LastApply did not advance (%d → %d) — the UI cannot tell the save finished", applied, got)
	}
}

// A config that DOES differ must still restart, or a real change would never reach
// the running process.
func TestApplyRestartsWhenTheConfigChanges(t *testing.T) {
	s := newTestSup(t)
	t.Cleanup(s.Stop)

	if err := s.Apply(applyCfg("warning")); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	waitFor(t, "xray running", s.Running)
	first := s.StartedAt()

	time.Sleep(1100 * time.Millisecond)

	if err := s.Apply(applyCfg("debug")); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	waitFor(t, "xray restarted", func() bool { return s.Running() && s.StartedAt() > first })
	if got := s.StartedAt(); got <= first {
		t.Errorf("a changed config did not restart Xray (start %d → %d)", first, got)
	}
}

// The skip requires a LIVE process, and boot is why.
//
// Every panel start reconciles, and on an unchanged install it regenerates exactly
// the config already sitting on disk from last time. A fresh supervisor knows nothing
// about a running process, so if the on-disk bytes could satisfy the check, Xray
// would never be started at all and the panel would come up serving nothing.
func TestApplyStartsXrayWhenTheOnDiskConfigAlreadyMatches(t *testing.T) {
	s := newTestSup(t)
	t.Cleanup(s.Stop)

	// Put the config on disk without ever starting a process, the state a freshly
	// booted panel finds.
	if err := s.Apply(applyCfg("warning")); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	waitFor(t, "xray running", s.Running)
	first := s.StartedAt()
	if first == 0 {
		t.Fatal("the seeding apply never started Xray")
	}

	// A second supervisor over the SAME config path, with nothing running: it must
	// start Xray rather than read the matching file and decide there is nothing to do.
	fresh := NewSupervisor(fakeXray(t), s.configPath, "")
	t.Cleanup(fresh.Stop)
	if fresh.Running() {
		t.Fatal("a brand new supervisor reports a running process")
	}
	if err := fresh.Apply(applyCfg("warning")); err != nil {
		t.Fatalf("apply on a fresh supervisor: %v", err)
	}
	waitFor(t, "xray started by the fresh supervisor", fresh.Running)
	if !fresh.Running() {
		t.Error("Xray was never started because the on-disk config already matched")
	}
}

// The user-sync path writes config.json ahead of the process ON PURPOSE (so a
// crash-restart reloads the right user set) and only then asks for a reconcile,
// because Xray cannot live-apply user changes to a Hysteria2 inbound — its
// authenticator is fixed when the inbound starts.
//
// So the skip must compare against the RUNNING config, never the file. Comparing
// files here reads "nothing changed" for the one change that still needs a restart,
// and a revoked user keeps tunnelling over QUIC indefinitely.
func TestApplyStillRestartsAfterWriteConfigMovedTheFile(t *testing.T) {
	s := newTestSup(t)
	t.Cleanup(s.Stop)

	if err := s.Apply(applyCfg("warning")); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	waitFor(t, "xray running", s.Running)
	first := s.StartedAt()

	time.Sleep(1100 * time.Millisecond)

	// What syncUsers does: the new config lands on disk, the process keeps the old one.
	next := applyCfg("debug")
	if err := s.WriteConfig(next); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := s.Apply(next); err != nil {
		t.Fatalf("apply after WriteConfig: %v", err)
	}
	waitFor(t, "xray restarted", func() bool { return s.Running() && s.StartedAt() > first })
	if got := s.StartedAt(); got <= first {
		t.Error("Apply skipped the restart because the file already matched — a removed " +
			"user would keep their Hysteria2 access")
	}
}
