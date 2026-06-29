package core

import (
	"strings"
	"testing"
)

func TestWorstStatus(t *testing.T) {
	cases := []struct {
		name   string
		in     []string
		expect string
	}{
		{"empty", nil, healthOK},
		{"all ok", []string{healthOK, healthOK}, healthOK},
		{"warn wins over ok", []string{healthOK, healthWarn, healthOK}, healthWarn},
		{"error wins over warn", []string{healthWarn, healthError, healthOK}, healthError},
		{"info never worsens", []string{healthOK, healthInfo}, healthOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checks := make([]HealthCheck, len(c.in))
			for i, s := range c.in {
				checks[i] = HealthCheck{Status: s}
			}
			if got := worstStatus(checks); got != c.expect {
				t.Fatalf("worstStatus = %q, want %q", got, c.expect)
			}
		})
	}
}

func TestDiskHealthThresholds(t *testing.T) {
	gb := int64(1) << 30
	cases := []struct {
		used, total int64
		want        string
	}{
		{0, 0, healthInfo},               // no data
		{20 * gb, 100 * gb, healthOK},    // 80% free
		{90 * gb, 100 * gb, healthWarn},  // 10% free
		{97 * gb, 100 * gb, healthError}, // 3% free
	}
	for _, c := range cases {
		if got := diskHealth(c.used, c.total).Status; got != c.want {
			t.Fatalf("diskHealth(%d/%d) = %q, want %q", c.used, c.total, got, c.want)
		}
	}
}

func TestCertWarnThreshold(t *testing.T) {
	cases := map[int]int{
		90: 14, // domain cert → warn under 14 days
		30: 10, // mid lifetime → lifetime/3
		7:  2,  // LE IP shortlived (~6-7d) → warn only under 2 days, not 14
		6:  2,
		3:  1,
		0:  14, // unknown lifetime → conservative default
	}
	for life, want := range cases {
		if got := certWarnThreshold(life); got != want {
			t.Fatalf("certWarnThreshold(%d) = %d, want %d", life, got, want)
		}
	}
}

func TestMemHealthThresholds(t *testing.T) {
	if got := memHealth(500, 1000).Status; got != healthOK {
		t.Fatalf("mem 50%% = %q, want ok", got)
	}
	if got := memHealth(950, 1000).Status; got != healthWarn {
		t.Fatalf("mem 95%% = %q, want warn", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		512:                    "512 Б",
		2 * 1024:               "2.0 КБ",
		5 * 1024 * 1024:        "5.0 МБ",
		3 * 1024 * 1024 * 1024: "3.0 ГБ",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Fatalf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	if got := humanDuration(0); got != "—" {
		t.Fatalf("humanDuration(0) = %q", got)
	}
	if got := humanDuration(90); !strings.HasPrefix(got, "1м") {
		t.Fatalf("humanDuration(90s) = %q, want minutes", got)
	}
	if got := humanDuration(3 * 3600); !strings.Contains(got, "ч") {
		t.Fatalf("humanDuration(3h) = %q, want hours", got)
	}
	if got := humanDuration(50 * 3600); !strings.Contains(got, "д") {
		t.Fatalf("humanDuration(50h) = %q, want days", got)
	}
}
