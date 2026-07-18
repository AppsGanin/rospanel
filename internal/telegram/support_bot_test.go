package telegram

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

func supportService(t *testing.T) *SupportService {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "support.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewSupport(nil, st)
}

// TestAdminReplyRouting covers the guards that decide whether a group message is
// somebody's support answer. Each case must return before touching Telegram, which
// a nil client asserts: reaching the API would panic.
func TestAdminReplyRouting(t *testing.T) {
	s := supportService(t)
	set := &model.Settings{TGSupportGroupID: -100999}
	if err := s.store.SetSupportTopic(555, 7, time.Now().Unix()); err != nil {
		t.Fatalf("SetSupportTopic: %v", err)
	}
	group := Chat{ID: -100999, Type: "supergroup", IsForum: true}

	cases := []struct {
		name string
		msg  *Message
	}{
		{"general thread is nobody's conversation",
			&Message{Chat: group, MessageID: 1, Text: "объявление"}},
		{"unknown topic was opened by hand",
			&Message{Chat: group, MessageID: 2, MessageThreadID: 42, Text: "заметка"}},
		{"internal note stays between admins",
			&Message{Chat: group, MessageID: 3, MessageThreadID: 7,
				Text: internalNotePrefix + " он писал на прошлой неделе"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s.handleAdminReply(context.Background(), nil, set, c.msg)
		})
	}
}

// TestHandleIgnoresForeignGroup: the bot may be a member of chats that aren't the
// support group. Relaying from one would leak an unrelated conversation into a
// user's chat, so anything but the configured group is dropped.
func TestHandleIgnoresForeignGroup(t *testing.T) {
	s := supportService(t)
	set := &model.Settings{TGSupportGroupID: -100999}
	s.handle(context.Background(), nil, set, Update{Message: &Message{
		Chat:            Chat{ID: -100111, Type: "supergroup", IsForum: true},
		MessageID:       1,
		MessageThreadID: 7,
		Text:            "привет",
	}})
}

func TestSupportRateLimit(t *testing.T) {
	s := supportService(t)
	now := time.Now()
	for i := range maxSupportPerWindow {
		if !s.allow(1, now) {
			t.Fatalf("message %d rejected inside the window", i+1)
		}
	}
	if s.allow(1, now) {
		t.Fatal("limit not enforced past the window budget")
	}
	// The limit is per chat: one flooder must not silence everyone else.
	if !s.allow(2, now) {
		t.Fatal("a different chat was rejected")
	}
	// A new window resets the budget.
	if !s.allow(1, now.Add(supportRateWindow)) {
		t.Fatal("window did not roll over")
	}
}

func TestTopicTitle(t *testing.T) {
	linked := model.User{ID: 42, Name: "Иван"}
	msg := &Message{Chat: Chat{ID: 555}, From: &User{ID: 555, Username: "vanya", FirstName: "Ваня"}}

	if got, want := topicTitle(linked, true, msg), "Иван · #42"; got != want {
		t.Errorf("linked = %q, want %q", got, want)
	}
	if got, want := topicTitle(model.User{}, false, msg), "@vanya"; got != want {
		t.Errorf("unlinked with username = %q, want %q", got, want)
	}
	noName := &Message{Chat: Chat{ID: 555}, From: &User{ID: 555, FirstName: "Ваня"}}
	if got, want := topicTitle(model.User{}, false, noName), "Ваня"; got != want {
		t.Errorf("unlinked without username = %q, want %q", got, want)
	}
	// Telegram rejects a name past its limit, which would cost the user their first
	// message — so a long panel name has to be cut, not passed through.
	long := model.User{ID: 1, Name: string(make([]byte, 300))}
	if got := topicTitle(long, true, msg); len(got) > topicNameMax {
		t.Errorf("title not truncated: %d chars", len(got))
	}
}

func TestErrorClassification(t *testing.T) {
	apiErr := func(code int, desc string) error {
		return &APIError{Code: code, Description: desc}
	}

	if !isThreadGone(apiErr(400, "Bad Request: message thread not found")) {
		t.Error("deleted topic not recognised — the conversation would stay dead")
	}
	if isThreadGone(apiErr(429, "Too Many Requests: retry after 5")) {
		t.Error("rate limit misread as a deleted topic")
	}
	if isThreadGone(nil) || isBlockedByUser(nil) {
		t.Error("nil error classified as a failure")
	}
	// A plain error carries no status code, so it can never be classified as
	// permanent — a network blip must stay retryable.
	if isThreadGone(errors.New("connection reset")) || isBlockedByUser(errors.New("connection reset")) {
		t.Error("a transport error classified as a permanent Telegram failure")
	}

	for _, e := range []error{
		apiErr(403, "Forbidden: bot was blocked by the user"),
		apiErr(403, "Forbidden: user is deactivated"),
		apiErr(400, "Bad Request: chat not found"),
	} {
		if !isBlockedByUser(e) {
			t.Errorf("not recognised as unreachable: %v", e)
		}
	}
	// Gating on the status code is what keeps a server-side hiccup from being
	// mistaken for a user who blocked the bot.
	if isBlockedByUser(apiErr(500, "Internal Server Error: chat not found")) {
		t.Error("a transient server error must not count as blocked")
	}
}
