package core

import (
	"fmt"
	"testing"
	"time"
)

// The throttle map is swept of entries past the throttle window — which throttle
// nothing — and swept at most once per window, so a map that stays over its cap with
// genuinely active pairs is not walked again on every sighting.
func TestAccessThrottleMapSweep(t *testing.T) {
	t.Parallel()
	m, _ := accessTestManager(t)
	now := time.Now().Unix()
	fill := func(age int64) {
		for i := range accLastMax + 100 {
			m.accLast[fmt.Sprintf("u%d|10.0.%d.%d", i, i/250, i%250)] = now - age
		}
	}

	// Stale entries (past the throttle window), and a sweep that is due: they go.
	fill(accThrottle + 1)
	m.accLastSwept = now - accThrottle
	m.RecordAccess("u900001", "198.51.100.9", "")
	if len(m.accLast) != 1 {
		t.Fatalf("after a due sweep the map holds %d entries — want only the new one", len(m.accLast))
	}

	// A sweep just ran: over the cap again, but the map is not walked this window.
	clear(m.accLast)
	fill(accThrottle + 1)
	m.accLastSwept = now
	m.RecordAccess("u900002", "198.51.100.9", "")
	if len(m.accLast) != accLastMax+101 {
		t.Fatalf("a sweep ran again inside its window: map holds %d", len(m.accLast))
	}

	// Entries inside the window still throttle — the sweep must not drop them.
	clear(m.accLast)
	fill(1)
	m.accLastSwept = now - accThrottle
	m.RecordAccess("u900003", "198.51.100.9", "")
	if len(m.accLast) != accLastMax+101 {
		t.Fatalf("a sweep dropped entries that are still throttling: map holds %d", len(m.accLast))
	}
	pending := len(m.accPending)
	m.RecordAccess("u1", "10.0.0.1", "") // entry u1|10.0.0.1 was set a second ago
	if len(m.accPending) != pending {
		t.Error("a pair seen a second ago was recorded again — the throttle is gone")
	}
}
