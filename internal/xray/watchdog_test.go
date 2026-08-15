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
	if f, act := s.watchdogTick(0); act || f != 0 {
		t.Fatalf("responsive process acted: (%d, %v)", f, act)
	}

	// A wedged process acts only after watchdogFailsToAct failures in a row.
	s = up()
	s.probe = wedged
	fails := 0
	for i := 1; i < watchdogFailsToAct; i++ {
		var act bool
		if fails, act = s.watchdogTick(fails); act {
			t.Fatalf("acted early at %d fails", fails)
		}
	}
	if _, act := s.watchdogTick(fails); !act {
		t.Fatalf("did not act after %d consecutive fails", watchdogFailsToAct)
	}

	// The cooldown blocks an immediate second restart even while still wedged.
	if _, act := s.watchdogTick(watchdogFailsToAct); act {
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
		if f, act := s.watchdogTick(watchdogFailsToAct); act || f != 0 {
			t.Errorf("%s supervisor was judged: (%d, %v)", name, f, act)
		}
	}
}
