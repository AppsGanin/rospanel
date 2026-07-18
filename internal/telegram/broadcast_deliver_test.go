package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Delivery fans out across several goroutines that share one *model.Broadcast and
// one SQLite connection. Sending the same person twice is the failure that cannot be
// taken back, so it is worth driving for real rather than reasoning about.
func TestDeliverSendsEachRecipientExactlyOnce(t *testing.T) {
	var mu sync.Mutex
	sends := map[int64]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			ChatID int64 `json:"chat_id"`
		}
		_ = json.Unmarshal(body, &payload)
		mu.Lock()
		sends[payload.ChatID]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "bc.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	const recipients = 24
	chats := make([]int64, recipients)
	for i := range chats {
		chats[i] = int64(1000 + i)
	}
	id, err := st.CreateBroadcast(&model.Broadcast{Text: "привет"}, 1700000000)
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	if err := st.AddBroadcastTargets(id, chats); err != nil {
		t.Fatalf("AddBroadcastTargets: %v", err)
	}

	s := NewBroadcast(st, t.TempDir())
	b, err := st.GetBroadcast(id)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	s.deliver(context.Background(), newTestClient(srv.URL+"/bot", "111:AAA"), b, chats)

	mu.Lock()
	defer mu.Unlock()
	if len(sends) != recipients {
		t.Fatalf("reached %d recipients, want %d", len(sends), recipients)
	}
	for chat, n := range sends {
		if n != 1 {
			t.Errorf("chat %d received %d messages, want exactly 1", chat, n)
		}
	}

	got, err := st.GetBroadcast(id)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	if got.Sent != recipients || got.Pending() != 0 || got.Failed != 0 {
		t.Fatalf("progress = %+v, want all %d sent", got, recipients)
	}
}

// A permanent refusal must be recorded as blocked and deactivate the subscriber, so
// every later broadcast stops spending a send slot on a chat that can never receive.
// A transient one must stay retryable.
func TestDeliverClassifiesFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			ChatID int64 `json:"chat_id"`
		}
		_ = json.Unmarshal(body, &payload)
		w.Header().Set("Content-Type", "application/json")
		switch payload.ChatID {
		case 2001:
			_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
		case 2002:
			_, _ = w.Write([]byte(`{"ok":false,"error_code":500,"description":"Internal Server Error"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}
	}))
	defer srv.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "bc.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	chats := []int64{2000, 2001, 2002}
	for _, c := range chats {
		if err := st.UpsertSubscriber(c, 0, "", "", "", 1700000000); err != nil {
			t.Fatalf("UpsertSubscriber: %v", err)
		}
	}
	id, err := st.CreateBroadcast(&model.Broadcast{Text: "привет"}, 1700000000)
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	if err := st.AddBroadcastTargets(id, chats); err != nil {
		t.Fatalf("AddBroadcastTargets: %v", err)
	}

	s := NewBroadcast(st, t.TempDir())
	b, _ := st.GetBroadcast(id)
	s.deliver(context.Background(), newTestClient(srv.URL+"/bot", "111:AAA"), b, chats)

	got, err := st.GetBroadcast(id)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	if got.Sent != 1 || got.Blocked != 1 || got.Failed != 1 {
		t.Fatalf("progress = sent %d / blocked %d / failed %d; want 1/1/1",
			got.Sent, got.Blocked, got.Failed)
	}

	blocked, err := st.SubscriberByChat(2001)
	if err != nil || blocked == nil {
		t.Fatalf("SubscriberByChat: %+v, %v", blocked, err)
	}
	if blocked.Active {
		t.Error("a chat that blocked the bot is still marked reachable")
	}
	// The transient failure must NOT deactivate anyone — that would quietly shrink
	// every future audience over a server hiccup.
	transient, err := st.SubscriberByChat(2002)
	if err != nil || transient == nil {
		t.Fatalf("SubscriberByChat: %+v, %v", transient, err)
	}
	if !transient.Active {
		t.Error("a transient 500 deactivated the subscriber")
	}
}
