package sub

import (
	"testing"
	"time"
)

func TestNextResetTime(t *testing.T) {
	// Anchor: Wed 2026-07-15 12:00 local.
	anchor := time.Date(2026, 7, 15, 12, 0, 0, 0, time.Local).Unix()

	cases := []struct {
		name   string
		period string
		want   string // dd.mm.yyyy, or "" when no reset
	}{
		{"none", "none", ""},
		{"empty", "", ""},
		{"days30 rolling", "days:30", "14.08.2026"},
		{"days3 rolling", "days:3", "18.07.2026"},
		{"days bad", "days:abc", ""},
		{"days zero", "days:0", ""},
		{"monthly boundary", "monthly", "01.08.2026"},
		{"yearly boundary", "yearly", "01.01.2027"},
		{"daily boundary", "daily", "16.07.2026"},
		{"weekly next monday", "weekly", "20.07.2026"}, // Mon after Wed 15th
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next, ok := nextResetTime(c.period, anchor)
			if c.want == "" {
				if ok {
					t.Fatalf("expected no reset, got %s", next.Format("02.01.2006"))
				}
				return
			}
			if !ok {
				t.Fatalf("expected reset %s, got none", c.want)
			}
			if got := next.Format("02.01.2006"); got != c.want {
				t.Fatalf("period %q: got %s, want %s", c.period, got, c.want)
			}
		})
	}

	// lastReset 0 ⇒ never scheduled regardless of period.
	if _, ok := nextResetTime("days:30", 0); ok {
		t.Fatal("lastReset 0 should yield no reset")
	}
}
