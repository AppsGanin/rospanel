package nodeagent

import (
	"testing"
	"time"
)

// The node's own address view is what turns the panel's "who is capped" into rules.
// It is fed by the access-log tap and, unlike the sync report buffer, must survive
// being read — a drained map would take the cap off everyone once a minute.
func TestSeenAddrsBuildRulesForCappedUsers(t *testing.T) {
	s := newSeenAddrs()
	now := time.Now()
	s.note("u1", "1.1.1.1", now)
	s.note("u1", "2.2.2.2", now)
	s.note("u9", "9.9.9.9", now) // not capped

	limits := map[string]int{"u1": 3000}
	rules := s.rules(limits, now)
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want one (only the capped user)", rules)
	}
	if rules[0].UserID != 1 || rules[0].Kbps != 3000 {
		t.Errorf("rule = %+v, want user 1 at 3000", rules[0])
	}
	if len(rules[0].IPs) != 2 {
		t.Errorf("addresses = %v, want both sightings", rules[0].IPs)
	}

	// Reading must not consume the view: the shaper runs every 30 seconds and would
	// otherwise install a cap and drop it again on the very next tick.
	if again := s.rules(limits, now); len(again) != 1 || len(again[0].IPs) != 2 {
		t.Errorf("second read = %+v, want the same rule", again)
	}
}

// A sighting older than the TTL stops holding the cap on that address, and a user
// with nothing left is forgotten entirely — otherwise the map grows for the life of
// the process.
func TestSeenAddrsExpiresOldSightings(t *testing.T) {
	s := newSeenAddrs()
	now := time.Now()
	s.note("u1", "1.1.1.1", now.Add(-shapeSightingTTL-time.Minute))
	s.note("u1", "2.2.2.2", now)

	rules := s.rules(map[string]int{"u1": 1000}, now)
	if len(rules) != 1 || len(rules[0].IPs) != 1 || rules[0].IPs[0] != "2.2.2.2" {
		t.Fatalf("rules = %+v, want only the fresh address", rules)
	}

	s2 := newSeenAddrs()
	s2.note("u1", "1.1.1.1", now.Add(-shapeSightingTTL-time.Minute))
	if rules := s2.rules(map[string]int{"u1": 1000}, now); len(rules) != 0 {
		t.Errorf("rules = %+v, want none once every sighting aged out", rules)
	}
	s2.mu.Lock()
	left := len(s2.seen)
	s2.mu.Unlock()
	if left != 0 {
		t.Errorf("%d users left in the map after their addresses expired", left)
	}
}

// Lifting a cap must lift it here too: the panel simply stops listing the user, and
// the next pass has to produce no rule for them.
func TestSeenAddrsDropsUncappedUsers(t *testing.T) {
	s := newSeenAddrs()
	now := time.Now()
	s.note("u1", "1.1.1.1", now)

	if rules := s.rules(map[string]int{"u1": 1000}, now); len(rules) != 1 {
		t.Fatalf("rules = %+v, want one", rules)
	}
	if rules := s.rules(map[string]int{}, now); len(rules) != 0 {
		t.Errorf("rules = %+v after the cap was lifted, want none", rules)
	}
	// A limit of zero means unlimited, not "cap at zero" — it must not produce a rule
	// that would throttle the user to nothing.
	if rules := s.rules(map[string]int{"u1": 0}, now); len(rules) != 0 {
		t.Errorf("rules = %+v for a zero cap, want none", rules)
	}
}
