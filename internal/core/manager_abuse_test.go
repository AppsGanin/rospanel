package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/abuse"
	"github.com/AppsGanin/rospanel/internal/store"
)

// abuseTestManager builds a manager with a store and a matcher primed with known-bad
// ranges. nodeTestManager doesn't set the abuse maps, so seed them here the same way
// New does.
func abuseTestManager(t *testing.T) (*Manager, int64) {
	t.Helper()
	m := nodeTestManager(t)
	m.abusePending = make(map[abusePendingKey]store.AbuseHit)
	m.abuseAlerted = make(map[abuseAlertKey]struct{})

	st := abuse.NewStore(t.TempDir())
	st.Matcher().SetIP(abuse.CatBadIP, []string{"203.0.113.0/24"})
	st.Matcher().SetIP(abuse.CatCustom, []string{"198.51.100.7"})
	m.abuse = st

	u, err := m.store.CreateUser("u1", "uuid1", "pw", "tok1", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return m, u.ID
}

func TestRecordAbuseMatchesAndFlushes(t *testing.T) {
	m, uid := abuseTestManager(t)

	// A clean address records nothing, and neither does a hostname — there are no
	// domain lists, so a name can never match.
	m.recordAbuse(uid, "8.8.8.8")
	m.recordAbuse(uid, "evil.example")
	// Listed addresses match, from both the feed and the operator's own list.
	m.recordAbuse(uid, "203.0.113.5")
	m.recordAbuse(uid, "203.0.113.9")
	m.recordAbuse(uid, "198.51.100.7")

	if got := len(m.abusePending); got == 0 {
		t.Fatal("nothing buffered for listed addresses")
	}
	m.FlushAbuse()
	if got := len(m.abusePending); got != 0 {
		t.Fatalf("buffer not drained: %d", got)
	}

	rows, err := m.store.AbuseByUser(uid, 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	seen := map[string]string{}
	for _, r := range rows {
		seen[r.Domain] = r.Category
	}
	if seen["8.8.8.8"] != "" || seen["evil.example"] != "" {
		t.Fatalf("clean traffic recorded as abuse: %+v", seen)
	}
	if seen["203.0.113.5"] != "badip" || seen["203.0.113.9"] != "badip" {
		t.Fatalf("feed matches wrong: %+v", seen)
	}
	if seen["198.51.100.7"] != "custom" {
		t.Fatalf("custom match wrong: %+v", seen)
	}
}

// TestRecordAbuseCountsPerAddress: repeat hits on one address roll up into a single
// row with a running count rather than a row each.
func TestRecordAbuseCountsPerAddress(t *testing.T) {
	m, uid := abuseTestManager(t)

	for range 3 {
		m.recordAbuse(uid, "203.0.113.5")
	}
	m.FlushAbuse()

	rows, err := m.store.AbuseByUser(uid, 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want one row, got %d: %+v", len(rows), rows)
	}
	if rows[0].Domain != "203.0.113.5" || rows[0].Count != 3 {
		t.Fatalf("rollup wrong: %+v", rows[0])
	}
}

// TestAbuseAlertThresholdAndDedup pins the alert contract: fire once a user crosses
// the DAILY threshold, and not again the same day. This is the fix for the review's
// highest finding — the threshold used to be a 14-day cumulative total, which
// re-alerted on the first hit of every subsequent day and paged the operator about
// every historical account after a restart.
func TestAbuseAlertThresholdAndDedup(t *testing.T) {
	m, uid := abuseTestManager(t)
	var alerts int
	m.SetAdminNotifier(func(string) { alerts++ })

	// Below the daily threshold: no alert.
	for range abuseAlertMin - 1 {
		m.recordAbuse(uid, "203.0.113.5")
	}
	// Repeat hits on one address roll into a single buffered row with a running
	// count, so flush once and check nothing fired.
	m.FlushAbuse()
	if alerts != 0 {
		t.Fatalf("alerted below threshold: %d", alerts)
	}

	// Cross the threshold: exactly one alert.
	for range 5 {
		m.recordAbuse(uid, "203.0.113.5")
	}
	m.FlushAbuse()
	if alerts != 1 {
		t.Fatalf("want 1 alert at threshold, got %d", alerts)
	}

	// More matches the same day: still just the one alert (dedup by user+day).
	for range 50 {
		m.recordAbuse(uid, "203.0.113.5")
	}
	m.FlushAbuse()
	if alerts != 1 {
		t.Fatalf("re-alerted the same day: %d", alerts)
	}
	// The per-day re-alert (a new day fires exactly once more) is covered by
	// TestAbuseAlertMidnightStraddleNoDuplicate, which drives explicit days.
}

// TestAbuseAlertMidnightStraddleNoDuplicate pins the fix for the non-monotonic
// marker: a flush batch that carries matches for two adjacent days must alert each
// day exactly once, regardless of map iteration order, and must not re-alert the
// later day on the following flush.
func TestAbuseAlertMidnightStraddleNoDuplicate(t *testing.T) {
	m, uid := abuseTestManager(t)
	perDay := map[string]int{}
	m.SetAdminNotifier(func(string) {})

	// Write both days' matches directly to the store so both cross the threshold,
	// then hand alertAbuse a batch that references both days.
	day1, day2 := "2026-07-20", "2026-07-21"
	var hits []store.AbuseHit
	for _, d := range []string{day1, day2} {
		hits = append(hits, store.AbuseHit{
			UserID: uid, Domain: "203.0.113.5", Category: "badip",
			Day: d, Count: int64(abuseAlertMin + 5), SeenAt: 100,
		})
	}
	if err := m.store.AddAbuseMatches(hits); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Count alerts per day by parsing nothing — instead track via the notifier count
	// across two runs, asserting the second run adds nothing.
	var alerts int
	m.SetAdminNotifier(func(string) { alerts++ })

	// Run alertAbuse many times with the straddling batch; map order varies per run,
	// so this exercises both iteration orders. Total distinct alerts must be exactly
	// 2 (one per day), never more.
	for range 50 {
		m.alertAbuse(hits)
	}
	if alerts != 2 {
		t.Fatalf("straddle produced %d alerts, want exactly 2 (one per day)", alerts)
	}
	_ = perDay
}

// TestRecordAbuseNilMatcherInert: no feeds loaded ⇒ the hot path does nothing.
func TestRecordAbuseNilMatcherInert(t *testing.T) {
	m := nodeTestManager(t)
	m.abusePending = make(map[abusePendingKey]store.AbuseHit)
	m.abuse = nil
	m.recordAbuse(1, "203.0.113.5") // must not panic or buffer
	if len(m.abusePending) != 0 {
		t.Fatal("buffered with no matcher")
	}
}
