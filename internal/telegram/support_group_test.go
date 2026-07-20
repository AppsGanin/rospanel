package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A message proves the bot is IN a group but says nothing about its rights. Writing
// "not an admin" on that basis sends the operator off to grant a permission the bot
// already has — so the rights are looked up, not guessed.
func TestGroupFromMessageLooksUpRights(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":777,"username":"help_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getChatMember"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"status":"administrator","can_manage_topics":true}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}
	}))
	defer srv.Close()

	s := supportService(t)
	client := newTestClient(srv.URL+"/bot", "111:AAA")
	set := &model.Settings{TGSupportGroupID: -100999}

	s.handle(context.Background(), client, set, Update{Message: &Message{
		Chat:      Chat{ID: -100111, Type: "supergroup", Title: "KVN3", IsForum: true},
		MessageID: 1,
		Text:      "привет",
	}})

	groups, err := s.store.ListSupportGroups()
	if err != nil {
		t.Fatalf("ListSupportGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want one", groups)
	}
	if !groups[0].IsAdmin {
		t.Fatal("an actual admin was recorded as not an admin")
	}
}

// If the rights lookup fails, the candidate must still be recorded — losing the
// group entirely is worse than showing it with an unverified label.
func TestGroupRecordedEvenIfRightsLookupFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error_code":500,"description":"boom"}`))
	}))
	defer srv.Close()

	s := supportService(t)
	client := newTestClient(srv.URL+"/bot", "111:AAA")
	s.rememberGroupFromMessage(context.Background(), client,
		Chat{ID: -100222, Type: "supergroup", Title: "Другая", IsForum: true})

	groups, err := s.store.ListSupportGroups()
	if err != nil || len(groups) != 1 || groups[0].ChatID != -100222 {
		t.Fatalf("groups = %+v, %v; want the group recorded anyway", groups, err)
	}
}
