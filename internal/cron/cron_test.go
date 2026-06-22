package cron

import (
	"testing"
	"time"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		t.Fatalf("bad time %q: %v", s, err)
	}
	return tm
}

func TestMatch(t *testing.T) {
	cases := []struct {
		expr string
		when string
		want bool
	}{
		{"0 3 * * *", "2026-06-22 03:00", true},   // daily 03:00 (a Monday)
		{"0 3 * * *", "2026-06-22 03:01", false},  // wrong minute
		{"0 3 * * *", "2026-06-22 04:00", false},  // wrong hour
		{"*/15 * * * *", "2026-06-22 12:30", true},
		{"*/15 * * * *", "2026-06-22 12:31", false},
		{"0 */6 * * *", "2026-06-22 12:00", true},  // 0,6,12,18
		{"0 */6 * * *", "2026-06-22 13:00", false},
		{"0 3 * * 1", "2026-06-22 03:00", true},    // Monday
		{"0 3 * * 1", "2026-06-23 03:00", false},   // Tuesday
		{"0 0 1 * 1", "2026-06-01 00:00", true},    // 1st (Monday too) — OR rule
		{"0 0 1 * 5", "2026-06-22 00:00", false},   // neither the 1st nor a Friday
		{"30 9 * * 1-5", "2026-06-22 09:30", true}, // weekday range
		{"30 9 * * 1-5", "2026-06-21 09:30", false}, // Sunday
		{"0 12 * * 0", "2026-06-21 12:00", true},   // Sunday as 0
		{"0 12 * * 7", "2026-06-21 12:00", true},   // Sunday as 7
	}
	for _, c := range cases {
		s, err := Parse(c.expr)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", c.expr, err)
			continue
		}
		if got := s.Match(at(t, c.when)); got != c.want {
			t.Errorf("%q @ %s = %v, want %v", c.expr, c.when, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{"", "0 3 * *", "0 3 * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "a * * * *", "*/0 * * * *"}
	for _, expr := range bad {
		if _, err := Parse(expr); err == nil {
			t.Errorf("Parse(%q) = nil error, want error", expr)
		}
	}
}
