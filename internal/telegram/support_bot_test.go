package telegram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

// stubAPI records which Bot API methods were called, so a test can assert what did
// NOT happen — the point of these guards is that nothing reaches the customer.
type stubAPI struct {
	mu     sync.Mutex
	calls  map[string]int
	server *httptest.Server
}

func newStubAPI(t *testing.T) *stubAPI {
	t.Helper()
	st := &stubAPI{calls: map[string]int{}}
	st.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		st.mu.Lock()
		st.calls[parts[len(parts)-1]]++
		st.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	t.Cleanup(st.server.Close)
	return st
}

func (s *stubAPI) client() *Client { return newTestClient(s.server.URL+"/bot", "111:AAA") }

func (s *stubAPI) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

// TestAdminReplyRouting covers the guards that decide whether a group message is
// somebody's support answer. What matters in every case is that copyMessage — the
// only call that reaches a customer — never fires.
func TestAdminReplyRouting(t *testing.T) {
	set := &model.Settings{TGSupportGroupID: -100999}
	group := Chat{ID: -100999, Type: "supergroup", IsForum: true}

	cases := []struct {
		name       string
		msg        *Message
		wantNotice bool // the thread is told why nothing was sent
	}{
		{
			name: "general thread is nobody's conversation",
			msg:  &Message{Chat: group, MessageID: 1, Text: "объявление"},
		},
		{
			name: "topic housekeeping is not a reply",
			msg: &Message{Chat: group, MessageID: 2, MessageThreadID: 7,
				ForumTopicEdited: &struct{}{}},
		},
		{
			// Silence here is what makes a dead thread indistinguishable from a
			// delivered answer, so this one must speak up.
			name:       "topic belongs to nobody",
			msg:        &Message{Chat: group, MessageID: 3, MessageThreadID: 42, Text: "заметка"},
			wantNotice: true,
		},
		{
			name: "internal note stays between admins",
			msg: &Message{Chat: group, MessageID: 4, MessageThreadID: 7,
				Text: internalNotePrefix + " он писал на прошлой неделе"},
			wantNotice: true,
		},
		{
			// The whole message is internal, not just the marked line — copyMessage
			// cannot edit — so the admin has to be told, or their answer vanishes.
			name: "note below an answer withholds the whole message",
			msg: &Message{Chat: group, MessageID: 5, MessageThreadID: 7,
				Text: "Здравствуйте, ключ обновлён\n" + internalNotePrefix + " напомнить про оплату"},
			wantNotice: true,
		},
		{
			name: "note in a caption counts too",
			msg: &Message{Chat: group, MessageID: 6, MessageThreadID: 7,
				Caption: internalNotePrefix + " клиент врёт",
				Photo:   []PhotoSize{{FileID: "x", Width: 90, Height: 90}}},
			wantNotice: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := supportService(t)
			if err := s.store.SetSupportTopic(-100999, 555, 7, time.Now().Unix()); err != nil {
				t.Fatalf("SetSupportTopic: %v", err)
			}
			api := newStubAPI(t)
			s.handleAdminReply(context.Background(), api.client(), set, c.msg)

			if n := api.count("copyMessage"); n != 0 {
				t.Fatalf("%d message(s) reached the customer", n)
			}
			if got := api.count("sendMessage") > 0; got != c.wantNotice {
				t.Fatalf("notice posted = %v, want %v", got, c.wantNotice)
			}
		})
	}
}

// An answer with no internal note must actually be delivered — the guards above must
// not have made the normal path unreachable.
func TestAdminReplyIsDelivered(t *testing.T) {
	s := supportService(t)
	if err := s.store.SetSupportTopic(-100999, 555, 7, time.Now().Unix()); err != nil {
		t.Fatalf("SetSupportTopic: %v", err)
	}
	api := newStubAPI(t)
	s.handleAdminReply(context.Background(), api.client(),
		&model.Settings{TGSupportGroupID: -100999},
		&Message{Chat: Chat{ID: -100999, Type: "supergroup", IsForum: true},
			MessageID: 9, MessageThreadID: 7, Text: "Здравствуйте, ключ обновлён"})

	if n := api.count("copyMessage"); n != 1 {
		t.Fatalf("copyMessage called %d times, want 1", n)
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
		if allowed, _ := s.allow(1, now); !allowed {
			t.Fatalf("message %d rejected inside the window", i+1)
		}
	}
	// The chat is told once and then goes quiet: answering every rejected message
	// would make a flood produce more outbound traffic than it did inbound.
	allowed, first := s.allow(1, now)
	if allowed || !first {
		t.Fatalf("crossing the limit = allowed %v, first %v; want false, true", allowed, first)
	}
	for range 50 {
		if allowed, first = s.allow(1, now); allowed || first {
			t.Fatalf("past the limit = allowed %v, first %v; want false, false", allowed, first)
		}
	}
	// The limit is per chat: one flooder must not silence everyone else.
	if allowed, _ = s.allow(2, now); !allowed {
		t.Fatal("a different chat was rejected")
	}
	// A new window resets the budget.
	if allowed, _ = s.allow(1, now.Add(supportRateWindow)); !allowed {
		t.Fatal("window did not roll over")
	}
}

// A topic an admin closed as "handled" must reopen, not stay shut: the relay keeps
// one thread per user forever, so a closed thread would end that user's support.
func TestClosedTopicIsDistinctFromDeleted(t *testing.T) {
	closed := &APIError{Code: 400, Description: "Bad Request: TOPIC_CLOSED"}
	if !isTopicClosed(closed) {
		t.Error("TOPIC_CLOSED not recognised — that user's support would be dead")
	}
	if isThreadGone(closed) {
		t.Error("a closed topic must not be recreated: it would strand the history")
	}
	gone := &APIError{Code: 400, Description: "Bad Request: message thread not found"}
	if isTopicClosed(gone) {
		t.Error("a deleted topic misread as merely closed — reopen would keep failing")
	}
}

// Update ids are per-bot. Carrying an offset across a token swap ACKs away the new
// bot's backlog and swallows messages until its counter catches up — silently.
func TestTokenSwapResetsOffset(t *testing.T) {
	s := supportService(t)
	s.clientFor("111:AAA")
	s.offset = 4000
	s.clientFor("222:BBB")
	if s.offset != 0 {
		t.Fatalf("offset = %d after a token change, want 0", s.offset)
	}
	s.offset = 12
	s.clientFor("222:BBB")
	if s.offset != 12 {
		t.Fatalf("offset reset on an unchanged token: %d", s.offset)
	}
}

// Discovery: the bot records groups it is in so the operator picks from a list.
// These are candidates only — the bot is reachable by @username, so anyone can add
// it to a group and land here; auto-applying would let a stranger redirect every
// support conversation to a chat they control.
func TestGroupDiscovery(t *testing.T) {
	s := supportService(t)

	s.trackGroup(&ChatMemberUpdated{
		Chat:          Chat{ID: -100777, Type: "supergroup", Title: "Поддержка", IsForum: true},
		NewChatMember: ChatMember{Status: "administrator", CanManageTopics: true},
	})
	groups, err := s.store.ListSupportGroups()
	if err != nil {
		t.Fatalf("ListSupportGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want one", groups)
	}
	g := groups[0]
	if g.ChatID != -100777 || g.Title != "Поддержка" || !g.IsForum || !g.IsAdmin {
		t.Fatalf("group recorded wrong: %+v", g)
	}

	// Being added as a plain member is still worth offering, but must be labelled
	// as unusable rather than silently promising to work.
	s.trackGroup(&ChatMemberUpdated{
		Chat:          Chat{ID: -100777, Type: "supergroup", Title: "Поддержка", IsForum: true},
		NewChatMember: ChatMember{Status: "member"},
	})
	if groups, _ = s.store.ListSupportGroups(); len(groups) != 1 || groups[0].IsAdmin {
		t.Fatalf("demotion not recorded: %+v", groups)
	}

	// Removed from the group → stop offering somewhere it can no longer post.
	s.trackGroup(&ChatMemberUpdated{
		Chat:          Chat{ID: -100777},
		NewChatMember: ChatMember{Status: "kicked"},
	})
	if groups, _ = s.store.ListSupportGroups(); len(groups) != 0 {
		t.Fatalf("group survived removal: %+v", groups)
	}
}

// A message in some other group is not relayed either way, but it is remembered —
// that covers groups the bot joined before discovery existed, whose my_chat_member
// event nobody was listening for.
func TestForeignGroupBecomesCandidate(t *testing.T) {
	s := supportService(t)
	set := &model.Settings{TGSupportGroupID: -100999}
	s.handle(context.Background(), nil, set, Update{Message: &Message{
		Chat:            Chat{ID: -100111, Type: "supergroup", Title: "Другая", IsForum: true},
		MessageID:       1,
		MessageThreadID: 7,
		Text:            "привет",
	}})
	groups, err := s.store.ListSupportGroups()
	if err != nil {
		t.Fatalf("ListSupportGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].ChatID != -100111 {
		t.Fatalf("groups = %+v, want the foreign group as a candidate", groups)
	}
	// And it must NOT have become the configured one.
	if set.TGSupportGroupID != -100999 {
		t.Fatal("a foreign group changed the configured one")
	}
}

func TestTopicTitleIsRuneSafe(t *testing.T) {
	// Telegram counts characters and rejects malformed UTF-8, so a byte-wise cut
	// through a multi-byte name would 400 on every message that user ever sends.
	long := model.User{ID: 1, Name: strings.Repeat("🙂", 200)}
	got := topicTitle(long, true, &Message{Chat: Chat{ID: 555}})
	if !utf8.ValidString(got) {
		t.Fatalf("title is not valid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > topicNameMax {
		t.Fatalf("title is %d characters, want at most %d", n, topicNameMax)
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
