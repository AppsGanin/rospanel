package nodeagent

import (
	"testing"
	"time"
)

// Every recurring agent loop that reaches the network is a beacon if its period
// never drifts. jitter keeps the average cadence while removing the frequency.
func TestJitterSpreadsWithoutDrifting(t *testing.T) {
	const d = statsInterval
	lo := d * (100 - jitterPct) / 100
	hi := d * (100 + jitterPct) / 100

	seen := map[time.Duration]bool{}
	var total time.Duration
	const draws = 2000
	for range draws {
		got := jitter(d)
		if got < lo || got > hi {
			t.Fatalf("jitter(%v) = %v, outside [%v, %v]", d, got, lo, hi)
		}
		seen[got] = true
		total += got
	}
	if len(seen) < 100 {
		t.Errorf("only %d distinct values in %d draws — not much of a spread", len(seen), draws)
	}
	// The mean must stay on the nominal interval, or jitter would quietly change
	// how often the agent samples rather than only when.
	mean := total / draws
	if off := mean - d; off > d/20 || off < -d/20 {
		t.Errorf("mean %v drifted from the nominal %v by more than 5%%", mean, d)
	}
}

// A zero or tiny interval must not turn into a negative delay (a Timer would fire
// instantly and spin).
func TestJitterHandlesTinyIntervals(t *testing.T) {
	for _, d := range []time.Duration{0, time.Nanosecond, time.Millisecond} {
		if got := jitter(d); got < 0 {
			t.Errorf("jitter(%v) = %v, want >= 0", d, got)
		}
	}
}
