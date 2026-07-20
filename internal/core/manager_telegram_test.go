package core

import "testing"

// Pasting a supergroup id by hand goes wrong exactly one way: Telegram shows the
// bare internal id, the API wants it prefixed with -100, and without the prefix
// every call reports the group as unreachable.
func TestNormalizeGroupID(t *testing.T) {
	if got, want := normalizeGroupID(1234567890), int64(-1001234567890); got != want {
		t.Errorf("bare id = %d, want %d", got, want)
	}
	// Already correct — must pass through untouched.
	if got := normalizeGroupID(-1001234567890); got != -1001234567890 {
		t.Errorf("prefixed id was rewritten to %d", got)
	}
	// A negative id is already in some -prefixed form; guessing which would risk
	// pointing support at a different chat, so it is left alone.
	if got := normalizeGroupID(-1234567890); got != -1234567890 {
		t.Errorf("negative id was rewritten to %d", got)
	}
	if got := normalizeGroupID(0); got != 0 {
		t.Errorf("zero = %d, want 0", got)
	}
}

// Topic mappings carry the group that issued them, so no save — of the same group,
// of a cleared field, or of a different group — can make one group's mapping answer
// for another. Both reviewers of this feature independently found the reset-on-change
// approach leaking messages across customers through the 0 state; this is the
// invariant that replaced it.
func TestTopicMappingsSurviveSavesAndStayScoped(t *testing.T) {
	m := bcManager(t)
	const groupA, groupB int64 = -1001111111111, -1002222222222

	save := func(g int64) {
		t.Helper()
		if err := m.SaveTelegramSupport(false, "555:CCC", "bot", g, ""); err != nil {
			t.Fatalf("save %d: %v", g, err)
		}
	}
	mappedIn := func(g int64) bool {
		t.Helper()
		id, err := m.store.SupportTopicByChat(g, 42)
		if err != nil {
			t.Fatalf("SupportTopicByChat: %v", err)
		}
		return id != 0
	}

	save(groupA)
	if err := m.store.SetSupportTopic(groupA, 42, 7, 1700000000); err != nil {
		t.Fatalf("SetSupportTopic: %v", err)
	}

	// Saving the form repeatedly — the same group, a cleared field, the same group
	// again — must never cost a live conversation.
	save(groupA)
	save(0)
	save(groupA)
	if !mappedIn(groupA) {
		t.Fatal("a save dropped a still-valid mapping — the conversation is unrecoverable")
	}

	// And the path that used to leak: A → 0 → B. The mapping still exists, but it
	// cannot answer for B, so nobody else's thread reaches this user.
	save(0)
	save(groupB)
	if mappedIn(groupB) {
		t.Fatal("group A's mapping answered for group B — replies would reach the wrong user")
	}
	if !mappedIn(groupA) {
		t.Fatal("group A's own mapping was lost")
	}
}
