package store

import (
	"path/filepath"
	"testing"
)

// TestSupportTopicMapping exercises the 0031 migration and both lookup directions:
// user → topic when they write, topic → user when an admin answers.
func TestSupportTopicMapping(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "support.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// An unknown chat reports 0 rather than an error — that's the signal to open a
	// topic, so it must not look like a failure.
	topicID, err := st.SupportTopicByChat(555)
	if err != nil || topicID != 0 {
		t.Fatalf("SupportTopicByChat on empty = %d, %v; want 0, nil", topicID, err)
	}
	chatID, err := st.SupportChatByTopic(7)
	if err != nil || chatID != 0 {
		t.Fatalf("SupportChatByTopic on empty = %d, %v; want 0, nil", chatID, err)
	}

	if err := st.SetSupportTopic(555, 7, 1700000000); err != nil {
		t.Fatalf("SetSupportTopic: %v", err)
	}
	if topicID, err = st.SupportTopicByChat(555); err != nil || topicID != 7 {
		t.Fatalf("SupportTopicByChat = %d, %v; want 7, nil", topicID, err)
	}
	if chatID, err = st.SupportChatByTopic(7); err != nil || chatID != 555 {
		t.Fatalf("SupportChatByTopic = %d, %v; want 555, nil", chatID, err)
	}

	// Re-pointing after the admins deleted a topic must move the mapping, not add a
	// second row — otherwise the reverse lookup would resolve to a dead thread.
	if err := st.SetSupportTopic(555, 9, 1700000100); err != nil {
		t.Fatalf("SetSupportTopic re-point: %v", err)
	}
	if topicID, err = st.SupportTopicByChat(555); err != nil || topicID != 9 {
		t.Fatalf("after re-point SupportTopicByChat = %d, %v; want 9, nil", topicID, err)
	}
	if chatID, err = st.SupportChatByTopic(7); err != nil || chatID != 0 {
		t.Fatalf("stale topic still resolves to %d; want 0", chatID)
	}

	if err := st.DeleteSupportTopic(555); err != nil {
		t.Fatalf("DeleteSupportTopic: %v", err)
	}
	if topicID, err = st.SupportTopicByChat(555); err != nil || topicID != 0 {
		t.Fatalf("after delete SupportTopicByChat = %d, %v; want 0, nil", topicID, err)
	}
}

// TestResetSupportTopics covers the group switch: thread ids belong to the group
// that issued them, so pointing support elsewhere must drop every mapping.
func TestResetSupportTopics(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "support-reset.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	for i := int64(1); i <= 3; i++ {
		if err := st.SetSupportTopic(100+i, i, 1700000000); err != nil {
			t.Fatalf("SetSupportTopic %d: %v", i, err)
		}
	}
	if err := st.ResetSupportTopics(); err != nil {
		t.Fatalf("ResetSupportTopics: %v", err)
	}
	for i := int64(1); i <= 3; i++ {
		if topicID, err := st.SupportTopicByChat(100 + i); err != nil || topicID != 0 {
			t.Fatalf("chat %d still mapped to %d", 100+i, topicID)
		}
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
