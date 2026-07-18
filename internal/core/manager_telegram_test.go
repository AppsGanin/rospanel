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

// Re-selecting the same group after the field was cleared must NOT wipe the topic
// mappings. Telegram offers no way to list a bot's topics, so an orphaned thread is
// unrecoverable: the next message from that user opens a SECOND topic with the same
// title, and the operator is left with two.
func TestGroupSaveKeepsTopicsUnlessGroupReallyChanges(t *testing.T) {
	m := bcManager(t)
	const group int64 = -1001111111111

	save := func(g int64) {
		t.Helper()
		if err := m.SaveTelegramSupport(false, "555:CCC", "bot", g, ""); err != nil {
			t.Fatalf("save %d: %v", g, err)
		}
	}
	mapped := func() bool {
		t.Helper()
		id, err := m.store.SupportTopicByChat(42)
		if err != nil {
			t.Fatalf("SupportTopicByChat: %v", err)
		}
		return id != 0
	}

	save(group)
	if err := m.store.SetSupportTopic(42, 7, 1700000000); err != nil {
		t.Fatalf("SetSupportTopic: %v", err)
	}

	save(group) // saving the same group again
	if !mapped() {
		t.Fatal("re-saving the same group dropped the mapping")
	}

	save(0) // operator clears the field
	if !mapped() {
		t.Fatal("clearing the field dropped the mapping")
	}

	save(group) // ...and picks the same group again
	if !mapped() {
		t.Fatal("re-selecting the same group dropped a still-valid mapping")
	}

	save(-1002222222222) // a genuinely different group
	if mapped() {
		t.Fatal("mapping survived a move to another group — replies would reach the wrong user")
	}
}
