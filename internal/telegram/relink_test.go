package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// relinkPanel is the little of the panel the link and sign-up paths ask for; it
// remembers whether the last sign-up was granted a trial.
type relinkPanel struct {
	Panel
	st    *store.Store
	trial *bool
}

func (relinkPanel) Location() *time.Location                            { return time.UTC }
func (relinkPanel) PlanName(int64) string                               { return "" }
func (relinkPanel) AuditTelegramLinked(context.Context, int64, string)  {}
func (relinkPanel) AuditTelegramDetached(context.Context, int64, int64) {}
func (relinkPanel) RegistrationBlacklisted(int64) bool                  { return false }
func (relinkPanel) AttachReferrer(context.Context, int64, int64)        {}
func (relinkPanel) PromosOfferedTo(int64) bool                          { return false }
func (p relinkPanel) RotateSubToken(_ context.Context, id int64) (*model.User, error) {
	if err := p.st.SetSubToken(id, "rotated-token"); err != nil {
		return nil, err
	}
	return p.st.GetUser(id)
}
func (p relinkPanel) CreateRegisteredUser(_ context.Context, name string, trial bool) (*model.User, error) {
	if p.trial != nil {
		*p.trial = trial
	}
	return p.st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
}

// A chat that already belongs to one account is asked before a link code moves it
// to another: the move takes the bot away from the first account.
func TestLinkCodeAsksBeforeMovingAChat(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p struct {
			Text        string          `json:"text"`
			ReplyMarkup json.RawMessage `json:"reply_markup"`
		}
		_ = json.Unmarshal(body, &p)
		mu.Lock()
		texts = append(texts, p.Text+" "+string(p.ReplyMarkup))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "relink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, _ := st.CreateUser("old-account", "uuid-a", "pw", "tok-a", 0, 0, 0)
	b, _ := st.CreateUser("web-account", "uuid-b", "pw", "tok-b", 0, 0, 0)
	const chat = 4242
	if err := st.SetUserTelegramChat(a.ID, chat); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTgLinkCode(b.ID, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	s := NewUser(relinkPanel{}, st)
	client := newTestClient(srv.URL+"/bot", "111:AAA")
	set := &model.Settings{}
	ctx := context.Background()

	s.linkUserFromCode(ctx, client, set, chat, "0123456789abcdef")
	if got, _ := st.GetUserByTelegramChatID(chat); got == nil || got.ID != a.ID {
		t.Fatal("the chat moved without being asked")
	}
	mu.Lock()
	asked := strings.Join(texts, "\n")
	mu.Unlock()
	if !strings.Contains(asked, "old-account") || !strings.Contains(asked, relinkPrefix+"0123456789abcdef") {
		t.Fatalf("no question naming the account and offering the move:\n%s", asked)
	}

	s.linkByCode(ctx, client, set, chat, "0123456789abcdef", true)
	if got, _ := st.GetUserByTelegramChatID(chat); got == nil || got.ID != b.ID {
		t.Fatalf("confirmed, the chat is on %v", got)
	}
	if got, _ := st.GetUser(a.ID); got.TgChatID != 0 {
		t.Fatal("the old account still holds the chat")
	}
	// The code is spent: pressing the button again changes nothing.
	s.linkByCode(ctx, client, set, chat, "0123456789abcdef", true)
	if got, _ := st.GetUserByTelegramChatID(chat); got == nil || got.ID != b.ID {
		t.Fatal("a spent code moved the chat")
	}
}

// An account moved to another Telegram from its page: refused while the operator
// keeps that off, asked about otherwise, and the Telegram it leaves is told.
func TestLinkCodeMovesAnAccountToAnotherTelegram(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	sent := map[int64][]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p struct {
			ChatID      int64           `json:"chat_id"`
			Text        string          `json:"text"`
			ReplyMarkup json.RawMessage `json:"reply_markup"`
		}
		_ = json.Unmarshal(body, &p)
		mu.Lock()
		sent[p.ChatID] = append(sent[p.ChatID], p.Text+" "+string(p.ReplyMarkup))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "rebind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := st.CreateUser("ann", "uuid-a", "pw", "tok-a", 0, 0, 0)
	const oldChat, newChat = 1001, 2002
	if err := st.SetUserTelegramChat(u.ID, oldChat); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTgLinkCode(u.ID, "fedcba9876543210"); err != nil {
		t.Fatal(err)
	}
	trial := true
	s := NewUser(relinkPanel{st: st, trial: &trial}, st)
	client := newTestClient(srv.URL+"/bot", "111:AAA")
	ctx := context.Background()

	s.linkByCode(ctx, client, &model.Settings{SubTGRebind: false}, newChat, "fedcba9876543210", true)
	if got, _ := st.GetUserByTelegramChatID(oldChat); got == nil || got.ID != u.ID {
		t.Fatal("moved while changing Telegram is switched off")
	}

	on := &model.Settings{SubTGRebind: true}
	s.linkUserFromCode(ctx, client, on, newChat, "fedcba9876543210")
	if got, _ := st.GetUserByTelegramChatID(oldChat); got == nil || got.ID != u.ID {
		t.Fatal("moved without being asked")
	}
	mu.Lock()
	asked := strings.Join(sent[newChat], "\n")
	mu.Unlock()
	if !strings.Contains(asked, relinkPrefix+"fedcba9876543210") || !strings.Contains(asked, "vu:cancel") {
		t.Fatalf("no question with a way back for a chat without an account:\n%s", asked)
	}

	on.Host = "vpn.example.com"
	s.linkByCode(ctx, client, on, newChat, "fedcba9876543210", true)
	if got, _ := st.GetUserByTelegramChatID(newChat); got == nil || got.ID != u.ID {
		t.Fatal("confirmed, the account did not move")
	}
	// The link it was taken with is dead; the new Telegram has the new one.
	if got, _ := st.GetUser(u.ID); got.SubToken != "rotated-token" {
		t.Fatalf("the subscription link was not reissued: %q", got.SubToken)
	}
	mu.Lock()
	gotLink := strings.Join(sent[newChat], "\n")
	mu.Unlock()
	if !strings.Contains(gotLink, "/rotated-token") {
		t.Fatalf("the new Telegram was not given the new link:\n%s", gotLink)
	}
	if got, _ := st.GetUserByTelegramChatID(oldChat); got != nil {
		t.Fatal("the old Telegram still holds the account")
	}
	mu.Lock()
	notice := strings.Join(sent[oldChat], "\n")
	mu.Unlock()
	if !strings.Contains(notice, "ann") {
		t.Fatalf("the old Telegram was not told:\n%s", notice)
	}

	// The Telegram the account left may sign up and buy — but without a second trial
	// when it signed itself up before, or every move would mint one.
	if err := st.MarkChatTrial(oldChat); err != nil {
		t.Fatal(err)
	}
	s.doRegister(ctx, client, oldChat, &model.Settings{TGUserRegMode: model.RegOpen, BillingEnabled: true}, "again")
	if got, _ := st.GetUserByTelegramChatID(oldChat); got == nil || got.Name != "again" {
		t.Fatal("the chat that gave its account away could not sign up")
	}
	if trial {
		t.Fatal("it was granted a second trial")
	}
	mu.Lock()
	welcome := strings.Join(sent[oldChat], "\n")
	mu.Unlock()
	if !strings.Contains(welcome, "Пробный период") && !strings.Contains(welcome, "trial") {
		t.Fatalf("not told why there is no trial:\n%s", welcome)
	}

	// Pressed in the Telegram it already sits in: nothing changes.
	if err := st.SetUserTgLinkCode(u.ID, "00112233aabbccdd"); err != nil {
		t.Fatal(err)
	}
	s.linkByCode(ctx, client, on, newChat, "00112233aabbccdd", false)
	if got, _ := st.GetUserByTelegramChatID(newChat); got == nil || got.ID != u.ID {
		t.Fatal("pressing it in its own Telegram moved the account")
	}
}

// A code confirmed twice at once moves the account once.
func TestLinkCodeIsClaimedOnce(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := st.CreateUser("bob", "uuid-b", "pw", "tok-b", 0, 0, 0)
	if err := st.SetUserTgLinkCode(u.ID, "aaaabbbbccccdddd"); err != nil {
		t.Fatal(err)
	}
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := st.ClaimUserTgLinkCode(u.ID, "aaaabbbbccccdddd"); err == nil && ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d claims won, want 1", wins)
	}
}

// A Telegram that never signed itself up — its account was linked by a code — gets
// the trial it never had, and is recorded as having had it from then on.
func TestFirstSignUpGetsATrialOnce(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "first.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	trial := false
	s := NewUser(relinkPanel{st: st, trial: &trial}, st)
	client := newTestClient(srv.URL+"/bot", "111:AAA")
	set := &model.Settings{TGUserRegMode: model.RegOpen, BillingEnabled: true}
	s.doRegister(context.Background(), client, 5005, set, "first")
	if !trial || !st.ChatHadTrial(5005) {
		t.Fatalf("first sign-up: trial=%v recorded=%v", trial, st.ChatHadTrial(5005))
	}
}
