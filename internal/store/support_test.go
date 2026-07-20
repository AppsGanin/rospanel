package store

import (
	"path/filepath"
	"testing"
)

// TestSupportTopicMapping exercises the 0031 migration and both lookup directions:
// user → topic when they write, topic → user when an admin answers.
const grp int64 = -100777

func TestSupportTopicMapping(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "support.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// An unknown chat reports 0 rather than an error — that's the signal to open a
	// topic, so it must not look like a failure.
	topicID, err := st.SupportTopicByChat(grp, 555)
	if err != nil || topicID != 0 {
		t.Fatalf("SupportTopicByChat on empty = %d, %v; want 0, nil", topicID, err)
	}
	chatID, err := st.SupportChatByTopic(grp, 7)
	if err != nil || chatID != 0 {
		t.Fatalf("SupportChatByTopic on empty = %d, %v; want 0, nil", chatID, err)
	}

	if err := st.SetSupportTopic(grp, 555, 7, 1700000000); err != nil {
		t.Fatalf("SetSupportTopic: %v", err)
	}
	if topicID, err = st.SupportTopicByChat(grp, 555); err != nil || topicID != 7 {
		t.Fatalf("SupportTopicByChat = %d, %v; want 7, nil", topicID, err)
	}
	if chatID, err = st.SupportChatByTopic(grp, 7); err != nil || chatID != 555 {
		t.Fatalf("SupportChatByTopic = %d, %v; want 555, nil", chatID, err)
	}

	// Re-pointing after the admins deleted a topic must move the mapping, not add a
	// second row — otherwise the reverse lookup would resolve to a dead thread.
	if err := st.SetSupportTopic(grp, 555, 9, 1700000100); err != nil {
		t.Fatalf("SetSupportTopic re-point: %v", err)
	}
	if topicID, err = st.SupportTopicByChat(grp, 555); err != nil || topicID != 9 {
		t.Fatalf("after re-point SupportTopicByChat = %d, %v; want 9, nil", topicID, err)
	}
	if chatID, err = st.SupportChatByTopic(grp, 7); err != nil || chatID != 0 {
		t.Fatalf("stale topic still resolves to %d; want 0", chatID)
	}

	if err := st.DeleteSupportTopic(grp, 555); err != nil {
		t.Fatalf("DeleteSupportTopic: %v", err)
	}
	if topicID, err = st.SupportTopicByChat(grp, 555); err != nil || topicID != 0 {
		t.Fatalf("after delete SupportTopicByChat = %d, %v; want 0, nil", topicID, err)
	}
}

// Mappings belong to the group that issued them. Thread ids are message ids, unique
// only within a chat, so a mapping must not answer for a different group — otherwise
// an admin replying in the new group's topic 7 reaches whoever owned topic 7 in the
// old one, and that user's next message lands in a stranger's thread.
func TestSupportTopicsAreScopedToTheirGroup(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "support-scope.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	const groupA, groupB int64 = -100111, -100222
	if err := st.SetSupportTopic(groupA, 555, 7, 1700000000); err != nil {
		t.Fatalf("SetSupportTopic: %v", err)
	}

	if id, err := st.SupportChatByTopic(groupB, 7); err != nil || id != 0 {
		t.Fatalf("group B resolved A's topic 7 to chat %d — a reply would reach the wrong user", id)
	}
	if id, err := st.SupportTopicByChat(groupB, 555); err != nil || id != 0 {
		t.Fatalf("group B reused A's topic %d — the message would land in a stranger's thread", id)
	}
	// The original group is unaffected, so re-pointing support back at it resumes
	// the existing conversations instead of orphaning them.
	if id, err := st.SupportChatByTopic(groupA, 7); err != nil || id != 555 {
		t.Fatalf("group A lost its own mapping: %d, %v", id, err)
	}

	// The same thread id may legitimately exist in both groups.
	if err := st.SetSupportTopic(groupB, 888, 7, 1700000100); err != nil {
		t.Fatalf("same thread id in another group rejected: %v", err)
	}
	if id, _ := st.SupportChatByTopic(groupB, 7); id != 888 {
		t.Fatalf("group B topic 7 = chat %d, want 888", id)
	}
	if id, _ := st.SupportChatByTopic(groupA, 7); id != 555 {
		t.Fatalf("group A topic 7 = chat %d, want 555", id)
	}

	// A stale row holding the thread id a new user was just issued must give way,
	// not wedge that user out of support via the unique index.
	if err := st.SetSupportTopic(groupA, 999, 7, 1700000200); err != nil {
		t.Fatalf("stale row blocked a new mapping: %v", err)
	}
	if id, _ := st.SupportChatByTopic(groupA, 7); id != 999 {
		t.Fatalf("group A topic 7 = chat %d, want 999", id)
	}
	if id, _ := st.SupportTopicByChat(groupA, 555); id != 0 {
		t.Fatalf("displaced chat still mapped to %d", id)
	}
}

// TestSupportSettingsRoundTrip covers the five settings columns 0031 adds, including
// the token's at-rest encryption and the cached bot username.
func TestSupportSettingsRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "support-settings.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	if set.TGSupportEnabled || set.TGSupportGroupID != 0 || set.TGSupportBotToken != "" {
		t.Fatalf("unexpected support defaults: %+v", set)
	}
	if link := set.SupportLink(); link != "" {
		t.Fatalf("SupportLink on defaults = %q, want empty", link)
	}

	if err := st.SetTelegramSupport(true, "555:CCC", "helpbot", -1001234567890, "Опишите проблему"); err != nil {
		t.Fatalf("SetTelegramSupport: %v", err)
	}
	set, err = st.GetSettings()
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if !set.TGSupportEnabled || set.TGSupportBotToken != "555:CCC" ||
		set.TGSupportBotUsername != "helpbot" || set.TGSupportGroupID != -1001234567890 ||
		set.TGSupportGreeting != "Опишите проблему" {
		t.Fatalf("support fields not persisted: %+v", set)
	}
	if got, want := set.SupportLink(), "https://t.me/helpbot"; got != want {
		t.Fatalf("SupportLink = %q, want %q", got, want)
	}

	// Disabled or username-less config must yield no link: the bots render the entry
	// point only for a non-empty one, so a dead button never reaches a user.
	set.TGSupportEnabled = false
	if link := set.SupportLink(); link != "" {
		t.Fatalf("SupportLink while disabled = %q, want empty", link)
	}
	set.TGSupportEnabled, set.TGSupportBotUsername = true, ""
	if link := set.SupportLink(); link != "" {
		t.Fatalf("SupportLink without username = %q, want empty", link)
	}
}
