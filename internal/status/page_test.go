package status

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
)

func TestDayClass(t *testing.T) {
	if got := DayClass(0, 1.0); got != "none" {
		t.Errorf("DayClass(0, 1.0) = %q; want none", got)
	}
	if got := DayClass(100, 1.0); got != "up" {
		t.Errorf("DayClass(100, 1.0) = %q; want up", got)
	}
	if got := DayClass(100, 0.999); got != "up" {
		t.Errorf("DayClass(100, 0.999) = %q; want up", got)
	}
	if got := DayClass(100, 0.95); got != "partial" {
		t.Errorf("DayClass(100, 0.95) = %q; want partial", got)
	}
	if got := DayClass(100, 0.85); got != "down" {
		t.Errorf("DayClass(100, 0.85) = %q; want down", got)
	}
}

func TestFormatUptime(t *testing.T) {
	if got := FormatUptime(99.954); got != "99.95%" {
		t.Errorf("FormatUptime(99.954) = %q; want 99.95%%", got)
	}
	if got := FormatUptime(100.0); got != "100.00%" {
		t.Errorf("FormatUptime(100.0) = %q; want 100.00%%", got)
	}
}

func TestRenderStatusPage(t *testing.T) {
	var d Data
	d.Theme("TestVPN", `{"accent":"#0d4cd3"}`, i18n.EN, 30)
	d.Headline = "All Systems Operational"
	d.HeadlineOK = true
	d.WindowLabel = "Last 30 days"
	d.UpdatedAt = "2026-08-26 12:00:00"
	d.Servers = []Server{
		{
			Name:   "Master Node",
			Up:     true,
			Uptime: "99.99%",
			Days: []Day{
				{Class: "up", Title: "2026-08-25: 100%"},
			},
		},
	}

	html, err := Render(d)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	htmlStr := string(html)
	if !strings.Contains(htmlStr, "TestVPN") {
		t.Errorf("rendered HTML missing brand name TestVPN")
	}
	if !strings.Contains(htmlStr, "Master Node") {
		t.Errorf("rendered HTML missing server name Master Node")
	}
	if !strings.Contains(htmlStr, d.L.AllUp) {
		t.Errorf("rendered HTML missing banner headline text %q", d.L.AllUp)
	}
}
