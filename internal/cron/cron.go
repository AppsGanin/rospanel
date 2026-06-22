// Package cron is a minimal standard 5-field cron parser/matcher (minute, hour,
// day-of-month, month, day-of-week). It supports "*", lists ("a,b"), ranges
// ("a-b"), and steps ("*/n", "a-b/n") — enough to express the panel's backup
// schedules without pulling in a dependency. Matching uses Vixie-cron semantics:
// when both day-of-month and day-of-week are restricted, a tick matches if EITHER
// does.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression. Each field is a bitmask of allowed values.
type Schedule struct {
	min, hour, dom, month, dow uint64
	domStar, dowStar           bool // field was literally "*" (drives the dom/dow OR rule)
}

// Parse compiles a 5-field cron expression. It returns an error for the wrong
// field count or any out-of-range / malformed field.
func Parse(expr string) (*Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields, got %d", len(fields))
	}
	s := &Schedule{}
	var err error
	if s.min, _, err = parseField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("cron minute: %w", err)
	}
	if s.hour, _, err = parseField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("cron hour: %w", err)
	}
	if s.dom, s.domStar, err = parseField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("cron day-of-month: %w", err)
	}
	if s.month, _, err = parseField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("cron month: %w", err)
	}
	// Day-of-week accepts 0–7 (both 0 and 7 are Sunday); fold 7 into 0.
	if s.dow, s.dowStar, err = parseField(fields[4], 0, 7); err != nil {
		return nil, fmt.Errorf("cron day-of-week: %w", err)
	}
	if s.dow&(1<<7) != 0 {
		s.dow = (s.dow &^ (1 << 7)) | 1 // 7 → Sunday(0)
	}
	return s, nil
}

// Match reports whether t (already in the desired timezone) satisfies the schedule.
func (s *Schedule) Match(t time.Time) bool {
	minOK := s.min&(1<<uint(t.Minute())) != 0
	hourOK := s.hour&(1<<uint(t.Hour())) != 0
	monthOK := s.month&(1<<uint(int(t.Month()))) != 0
	domBit := s.dom&(1<<uint(t.Day())) != 0
	dowBit := s.dow&(1<<uint(int(t.Weekday()))) != 0

	var dayOK bool
	switch {
	case s.domStar && s.dowStar:
		dayOK = true
	case s.domStar: // only DOW restricted
		dayOK = dowBit
	case s.dowStar: // only DOM restricted
		dayOK = domBit
	default: // both restricted → OR
		dayOK = domBit || dowBit
	}
	return minOK && hourOK && monthOK && dayOK
}

// parseField compiles one comma-separated cron field into a bitmask, also
// reporting whether it was the literal "*".
func parseField(field string, min, max int) (mask uint64, star bool, err error) {
	star = field == "*"
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return 0, false, fmt.Errorf("empty term")
		}
		lo, hi, step := min, max, 1
		body := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			body = part[:i]
			if step, err = strconv.Atoi(part[i+1:]); err != nil || step < 1 {
				return 0, false, fmt.Errorf("bad step %q", part)
			}
		}
		switch {
		case body == "*":
			// keep lo..hi
		case strings.ContainsRune(body, '-'):
			r := strings.SplitN(body, "-", 2)
			if lo, err = strconv.Atoi(r[0]); err != nil {
				return 0, false, fmt.Errorf("bad range %q", part)
			}
			if hi, err = strconv.Atoi(r[1]); err != nil {
				return 0, false, fmt.Errorf("bad range %q", part)
			}
		default:
			if lo, err = strconv.Atoi(body); err != nil {
				return 0, false, fmt.Errorf("bad value %q", part)
			}
			if !strings.Contains(part, "/") {
				hi = lo // a single value (no step) matches just itself
			}
		}
		if lo < min || hi > max || lo > hi {
			return 0, false, fmt.Errorf("value out of range %d-%d in %q", min, max, part)
		}
		for v := lo; v <= hi; v += step {
			mask |= 1 << uint(v)
		}
	}
	return mask, star, nil
}
