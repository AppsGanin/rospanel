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
	fresh := mkUser(t, m, "fresh", 0)
	soon := mkUser(t, m, "soon", now.Add(48*time.Hour).Unix())

	// "never" registered long ago and still hasn't shown up — the case the filter is
	// for. "fresh" registered minutes ago; a "вы не заходили 30 дней" message would be
	// nonsense to them, so the account's own age has to floor the filter.
	if err := m.store.BackdateUserForTest(never, now.Add(-60*24*time.Hour)); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	for _, c := range []struct {
		chat, user int64
		lastSeen   int64
	}{
		{100, online, now.Add(-time.Hour).Unix()},
		{200, stale, now.Add(-10 * 24 * time.Hour).Unix()},
		{300, never, 0},
		{350, fresh, 0},
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
		{model.AudienceAll, []int64{100, 200, 300, 350, 400, 500}},
		{model.AudienceLinked, []int64{100, 200, 300, 350, 400}},
		{model.AudienceUnlinked, []int64{500}},
		{model.AudienceNever, []int64{300, 350}},
		{"seen:1", []int64{100, 400}},
		{"seen:30", []int64{100, 200, 400}},
		// Never-connected counts as not seen — but only once the ACCOUNT is older
		// than the horizon, so today's signups aren't told they've been away.
		{"unseen:7", []int64{200, 300}},
		{"unseen:90", nil},
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

// A deleted account leaves its subscriber row behind on purpose — the person is
// still in the bot, and reaching them is what the "без аккаунта" audience is for. But
// the row must stop naming the account, or the filters read a missing user's zero
// values as facts: "ни разу не подключался" collected ex-customers who connected
// yesterday, while the audience meant to hold them excluded them.
func TestDeletedAccountBecomesUnlinked(t *testing.T) {
	m := bcManager(t)
	id := mkUser(t, m, "ушёл", 0)
	sub(t, m, 900, id)
	if err := m.store.TouchLastSeen(id, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("seen: %v", err)
	}
	if err := m.store.DeleteUser(id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	for _, tc := range []struct {
		audience string
		want     bool
	}{
		{model.AudienceAll, true},
		{model.AudienceUnlinked, true}, // the audience documented to hold them
		{model.AudienceLinked, false},  // there is no account to be linked to
		{model.AudienceNever, false},   // they connected an hour ago
		{"unseen:7", false},            // ...so they are not a dormant account
		{model.AudienceActive, false},
	} {
		t.Run(tc.audience, func(t *testing.T) {
			chats, err := m.audienceChats(tc.audience)
			if err != nil {
				t.Fatalf("audienceChats: %v", err)
			}
			got := len(chats) == 1 && chats[0] == 900
			if got != tc.want {
				t.Fatalf("included = %v, want %v (chats %v)", got, tc.want, chats)
			}
		})
	}
}

// The preview has to agree with the send. An unrecognised audience resolved to an
// empty list and previewed as "0 получателей", while the launch would either refuse
// it or — for an empty string — fall back to everyone.
func TestAudiencePreviewMatchesTheSend(t *testing.T) {
	m := bcManager(t)
	sub(t, m, 100, 0)

	if n, err := m.AudiencePreview(""); err != nil || n != 1 {
		t.Fatalf("empty audience previewed %d, %v; want the same 1 recipient the send would use", n, err)
	}
	for _, bad := range []string{"nonsense", "seen:0", "seen:9999"} {
		if _, err := m.AudiencePreview(bad); err == nil {
			t.Errorf("%q previewed a count instead of being refused", bad)
		}
	}
}
