package core

import (
	"testing"
	"time"
)

func TestResetDueRollingDays(t *testing.T) {
	loc := time.UTC
	const day = int64(86400)
	base := int64(1_700_000_000)

	cases := []struct {
		name      string
		period    string
		lastReset int64
		now       int64
		want      bool
	}{
		{"days30 not elapsed", "days:30", base, base + 29*day, false},
		{"days30 exactly elapsed", "days:30", base, base + 30*day, true},
		{"days30 well past", "days:30", base, base + 45*day, true},
		{"days3 elapsed", "days:3", base, base + 3*day, true},
		{"days3 not elapsed", "days:3", base, base + 2*day, false},
		{"never reset anchors off", "days:30", 0, base + 100*day, false},
		{"zero days never resets", "days:0", base, base + 100*day, false},
		{"garbage never resets", "days:abc", base, base + 100*day, false},
		{"none never resets", "none", base, base + 100*day, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resetDue(c.period, c.lastReset, c.now, loc); got != c.want {
				t.Fatalf("resetDue(%q, last=%d, now=%d) = %v, want %v",
					c.period, c.lastReset, c.now, got, c.want)
			}
		})
	}
}
