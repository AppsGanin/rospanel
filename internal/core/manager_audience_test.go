package core

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Targeted audiences are what let an operator write to the people a message is
// actually for. Each filter is checked against the same population, so a filter that
// silently matches everyone (or nobody) shows up as a wrong list rather than a
// plausible-looking count.
func TestAudienceTargeting(t *testing.T) {
	m := bcManager(t)
	now := time.Now()

	// chat 100: active, online an hour ago
	// chat 200: active, last seen 10 days ago
	// chat 300: linked but never connected
	// chat 400: expires in 2 days
	// chat 500: no account at all
	online := mkUser(t, m, "online", 0)
	stale := mkUser(t, m, "stale", 0)
	never := mkUser(t, m, "never", 0)
	soon := mkUser(t, m, "soon", now.Add(48*time.Hour).Unix())

	for _, c := range []struct {
		chat, user int64
		lastSeen   int64
	}{
		{100, online, now.Add(-time.Hour).Unix()},
		{200, stale, now.Add(-10 * 24 * time.Hour).Unix()},
		{300, never, 0},
		{400, soon, now.Add(-time.Hour).Unix()},
	} {
		sub(t, m, c.chat, c.user)
		if c.lastSeen != 0 {
			if err := m.store.TouchLastSeen(c.user, c.lastSeen); err != nil {
				t.Fatalf("seen: %v", err)
			}
		}
	}
	sub(t, m, 500, 0)

	for _, tc := range []struct {
		audience string
		want     []int64
	}{
		{model.AudienceAll, []int64{100, 200, 300, 400, 500}},
		{model.AudienceLinked, []int64{100, 200, 300, 400}},
		{model.AudienceUnlinked, []int64{500}},
		{model.AudienceNever, []int64{300}},
		{"seen:1", []int64{100, 400}},
		{"seen:30", []int64{100, 200, 400}},
		// Never-connected counts as not seen: someone who never arrived is the
		// clearest case of what this filter is looking for.
		{"unseen:7", []int64{200, 300}},
		{"expiring:3", []int64{400}},
		{"expiring:1", nil},
	} {
		t.Run(tc.audience, func(t *testing.T) {
			got, err := m.audienceChats(tc.audience)
			if err != nil {
				t.Fatalf("audienceChats: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// A malformed or out-of-range horizon must be refused, not silently treated as a
// filter that matches nobody — an empty audience reads as "nobody qualifies" when the
// truth is "the panel didn't understand you".
func TestAudienceValidation(t *testing.T) {
	for _, bad := range []string{"seen:", "seen:0", "seen:abc", "seen:9999", "nonsense", "unseen:-1"} {
		if model.ValidAudience(bad) {
			t.Errorf("%q accepted as an audience", bad)
		}
	}
	for _, good := range []string{"all", "never", "seen:1", "unseen:30", "expiring:7"} {
		if !model.ValidAudience(good) {
			t.Errorf("%q rejected", good)
		}
	}
}
