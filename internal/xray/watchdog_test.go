package xray

import (
	"testing"
	"time"
)

func TestWatchdogTick(t *testing.T) {
	up := func() *Supervisor { return &Supervisor{cur: &proc{started: time.Now()}} }
	responsive := func() bool { return true }
	wedged := func() bool { return false }

	// A responsive process never acts and keeps the counter at zero.
	s := up()
	s.probe = responsive
	if f, alert, restart := s.watchdogTick(0); alert || restart || f != 0 {
		t.Fatalf("responsive process acted: (%d, alert=%v, restart=%v)", f, alert, restart)
	}

	// A wedged process (default: watchdog enabled) acts only after watchdogFailsToAct
	// failures in a row, and then both alerts AND restarts.
	s = up()
	s.probe = wedged
	fails := 0
	for i := 1; i < watchdogFailsToAct; i++ {
		var alert bool
		if fails, alert, _ = s.watchdogTick(fails); alert {
			t.Fatalf("acted early at %d fails", fails)
		}
	}
	if _, alert, restart := s.watchdogTick(fails); !alert || !restart {
		t.Fatalf("enabled watchdog did not restart after %d fails (alert=%v restart=%v)",
			watchdogFailsToAct, alert, restart)
	}

	// The cooldown blocks an immediate second action even while still wedged.
	if _, alert, restart := s.watchdogTick(watchdogFailsToAct); alert || restart {
		t.Fatal("acted again inside the cooldown window — restart storm not prevented")
	}

	// A down, suspended, or mid-bounce supervisor is never judged (and resets the
	// counter): a routine restart must not read as a wedge.
	for name, mut := range map[string]func(*Supervisor){
		"down":       func(s *Supervisor) { s.cur = nil },
		"suspended":  func(s *Supervisor) { s.suspended = true },
		"restarting": func(s *Supervisor) { s.restarting = true },
	} {
		s := up()
		s.probe = wedged
		mut(s)
		if f, alert, restart := s.watchdogTick(watchdogFailsToAct); alert || restart || f != 0 {
			t.Errorf("%s supervisor was judged: (%d, alert=%v, restart=%v)", name, f, alert, restart)
		}
	}
}

// With auto-recovery OFF the watchdog still DETECTS and ALERTS on a wedge — it just
// doesn't restart, and no restart is counted. Turning off the auto-restart must not blind
// the operator to the outage.
func TestWatchdogDisabledAlertsButDoesNotRestart(t *testing.T) {
	s := &Supervisor{cur: &proc{started: time.Now()}}
	s.probe = func() bool { return false } // wedged
	s.SetWatchdogEnabled(false)

	fails := 0
	var alert, restart bool
	for range watchdogFailsToAct {
		fails, alert, restart = s.watchdogTick(fails)
	}
	if !alert {
		t.Fatal("disabled watchdog did not alert on a wedge")
	}
	if restart {
		t.Fatal("disabled watchdog restarted the process")
	}
	if _, n, _ := s.WatchdogStats(); n != 0 {
		t.Fatalf("WatchdogStats restarts = %d while disabled, want 0", n)
	}

	// Re-enable and clear the cooldown the alert just set; now it restarts and counts it.
	s.SetWatchdogEnabled(true)
	s.mu.Lock()
	s.lastWatchdog = time.Time{}
	s.mu.Unlock()
	fails = 0
	for range watchdogFailsToAct {
		_, alert, restart = s.watchdogTick(fails)
		fails++
	}
	if !alert || !restart {
		t.Fatalf("watchdog did not restart after being re-enabled (alert=%v restart=%v)", alert, restart)
	}
	if _, n, _ := s.WatchdogStats(); n != 1 {
		t.Fatalf("WatchdogStats restarts = %d, want 1", n)
	}
}
