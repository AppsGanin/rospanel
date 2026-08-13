package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A day with no traffic writes no row, so the stored series is a list of busy days
// rather than a time series. Anything that averages over a window reads the gaps as
// missing rather than as zero and overstates the quiet periods.
func TestFillDaysTurnsBusyDaysIntoATimeSeries(t *testing.T) {
	got := fillDays([]model.DailyPoint{
		{Day: "2026-07-30", Up: 10, Down: 20},
		{Day: "2026-08-02", Up: 30, Down: 40},
	}, "2026-07-29", "2026-08-03")

	want := []model.DailyPoint{
		{Day: "2026-07-29"},
		{Day: "2026-07-30", Up: 10, Down: 20},
		{Day: "2026-07-31"}, // month boundary crossed by the calendar, not by string maths
		{Day: "2026-08-01"},
		{Day: "2026-08-02", Up: 30, Down: 40},
		{Day: "2026-08-03"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d points, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A single day is a range too, and an empty one still has to answer with that day.
func TestFillDaysCoversTheEdges(t *testing.T) {
	if got := fillDays(nil, "2026-08-13", "2026-08-13"); len(got) != 1 || got[0].Day != "2026-08-13" {
		t.Errorf("one empty day = %+v", got)
	}
	// A window that cannot be parsed is the caller's to reject; inventing days for it
	// would turn a bad request into plausible-looking data.
	stored := []model.DailyPoint{{Day: "2026-08-13", Up: 1}}
	if got := fillDays(stored, "not-a-date", "2026-08-13"); len(got) != 1 || got[0].Up != 1 {
		t.Errorf("unparseable from = %+v, want the stored rows untouched", got)
	}
	if got := fillDays(stored, "2026-08-13", "2026-08-01"); len(got) != 1 || got[0].Up != 1 {
		t.Errorf("reversed range = %+v, want the stored rows untouched", got)
	}
	// Padding must not be able to dwarf the data.
	if got := fillDays(stored, "2000-01-01", "2026-08-13"); len(got) != 1 {
		t.Errorf("a 26-year window materialised %d points", len(got))
	}
}
