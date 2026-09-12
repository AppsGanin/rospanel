package model_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Several screens state a retention window in days — "совпадений за 30 дней", "хранится
// 90 дней" — and the number is a copy of a Go constant, because the SPA has no other
// way to know it. A copy that is not tied to its source drifts the moment the source
// changes, and it did: blocklist matches went from 14 to 30 days, the statistics page
// was updated, and the dashboard kept its own 14 — counting a fortnight and saying so
// beside a page counting a month.
//
// So each copy is read from its file and held to the constant it claims to mirror.
// There must be exactly one copy per file too: two in one file is how the dashboard's
// stale one hid.
func TestRetentionMirrorsMatchTheirConstants(t *testing.T) {
	for _, m := range []struct {
		file, name string
		want       int
	}{
		{"AbuseList.tsx", "ABUSE_WINDOW_DAYS", model.AbuseRetentionDays},
		{"CountryMap.tsx", "WINDOW_DAYS", model.ConnectionRetentionDays},
		{"EventsPanel.tsx", "RETENTION_DAYS", model.UserEventRetentionDays},
		{"AdminAuditPanel.tsx", "RETENTION_DAYS", model.AdminAuditRetentionDays},
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", m.file))
		if err != nil {
			t.Skipf("frontend not available (%v)", err)
		}
		re := regexp.MustCompile(`(?m)^(?:export )?const ` + m.name + ` = (\d+)`)
		found := re.FindAllSubmatch(b, -1)
		if len(found) != 1 {
			t.Errorf("%s: want exactly one `const %s = N`, found %d", m.file, m.name, len(found))
			continue
		}
		if got, _ := strconv.Atoi(string(found[0][1])); got != m.want {
			t.Errorf("%s: %s = %d, but the constant it mirrors is %d", m.file, m.name, got, m.want)
		}
	}
}

// No other file keeps its own copy of the blocklist window: the dashboard's private
// ABUSE_DAYS is what went stale, and the fix was to import the one mirror.
func TestTheBlocklistWindowHasOneMirror(t *testing.T) {
	dir := filepath.Join("..", "..", "web", "src")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("frontend not available (%v)", err)
	}
	stray := regexp.MustCompile(`(?m)^(?:export )?const ABUSE_\w*DAYS = \d+`)
	for _, e := range entries {
		if e.IsDir() || e.Name() == "AbuseList.tsx" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if stray.Match(b) {
			t.Errorf("%s keeps its own blocklist window — import ABUSE_WINDOW_DAYS from AbuseList.tsx", e.Name())
		}
	}
}
