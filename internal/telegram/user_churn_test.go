package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// churnPanel records the answers the bot hands over; closed refuses them, as the
// panel does once the survey is off or the user is back on a plan.
type churnPanel struct {
	Panel
	mu      *sync.Mutex
	answers *[]string
	closed  bool
}

func (p churnPanel) AnswerChurn(_ context.Context, _ int64, reason, comment, source string) error {
	if p.closed {
		return errors.New("closed")
	}
	p.mu.Lock()
	*p.answers = append(*p.answers, reason+"|"+comment+"|"+source)
	p.mu.Unlock()
	return nil
}
func (churnPanel) PluginBot() core.PluginBot { return nil }

// The reasons arrive as buttons; one is taken at once, "other" asks for words and
// the next message carries them; a closed survey says so instead of thanking.
func TestChurnSurveyInTheBot(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p struct {
			Text        string          `json:"text"`
			ReplyMarkup json.RawMessage `json:"reply_markup"`
		}
		_ = json.Unmarshal(body, &p)
		mu.Lock()
		if p.Text != "" {
			sent = append(sent, p.Text+" "+string(p.ReplyMarkup))
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(sent) == 0 {
			return ""
		}
		return sent[len(sent)-1]
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "churn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := st.CreateUser("left", "uuid-l", "pw", "tok-l", 0, 0, 0)
	const chat = 5151
	if err := st.SetUserTelegramChat(u.ID, chat); err != nil {
		t.Fatal(err)
	}
	var answers []string
	s := NewUser(churnPanel{mu: &mu, answers: &answers}, st)
	client := newTestClient(srv.URL+"/bot", "111:AAA")
	ctx := context.Background()
	set, _ := st.GetSettings()
	user, _ := st.GetUser(u.ID)

	// Every reason is offered, as a button carrying it.
	for _, r := range model.ChurnReasons {
		found := false
		for _, row := range churnRows(i18n.RU) {
			if row[0].CallbackData == churnPrefix+r && row[0].Text == i18n.T(i18n.RU, sub.ChurnLabelKeys[r]) {
				found = true
			}
		}
		if !found {
			t.Errorf("reason %q has no button", r)
		}
	}

	cb := func(data string) {
		s.handleUserCallback(ctx, client, &CallbackQuery{ID: "1", Data: data,
			Message: &Message{MessageID: 9, Chat: Chat{ID: chat, Type: "private"}}}, set, *user)
	}
	cb(churnPrefix + model.ChurnPrice)
	if !strings.Contains(last(), i18n.T(i18n.RU, "churn.thanks")) {
		t.Fatalf("no thanks after a reason: %q", last())
	}
	cb(churnPrefix + model.ChurnOther)
	if !strings.Contains(last(), i18n.T(i18n.RU, "churn.otherAsk")) {
		t.Fatalf("other did not ask for words: %q", last())
	}
	s.handleMessage(ctx, client, &Message{MessageID: 10, Chat: Chat{ID: chat, Type: "private"}, Text: "  слишком медленно "})
	if !strings.Contains(last(), i18n.T(i18n.RU, "churn.thanks")) {
		t.Fatalf("no thanks after the words: %q", last())
	}
	mu.Lock()
	got := strings.Join(answers, ";")
	mu.Unlock()
	if got != "price||bot;other||bot;other|слишком медленно|bot" {
		t.Fatalf("answers = %q", got)
	}

	// Closed: the button says so rather than thanking.
	closed := NewUser(churnPanel{mu: &mu, answers: &answers, closed: true}, st)
	closed.handleUserCallback(ctx, client, &CallbackQuery{ID: "2", Data: churnPrefix + model.ChurnPrice,
		Message: &Message{MessageID: 11, Chat: Chat{ID: chat, Type: "private"}}}, set, *user)
	if !strings.Contains(last(), i18n.T(i18n.RU, "churn.closed")) {
		t.Fatalf("a closed survey answered %q", last())
	}
}
