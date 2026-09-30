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

// relinkPanel is the little of the panel the link path asks for.
type relinkPanel struct{ Panel }

func (relinkPanel) Location() *time.Location                           { return time.UTC }
func (relinkPanel) PlanName(int64) string                              { return "" }
func (relinkPanel) AuditTelegramLinked(context.Context, int64, string) {}

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
