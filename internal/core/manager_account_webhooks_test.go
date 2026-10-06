package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// An outside system keeping its own copy of an account hears when the link it holds
// dies and when the account's Telegram changes — however that happened.
func TestAccountChangeWebhooks(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "hooks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st, tz: time.UTC, webhookCh: make(chan webhookJob, 64)}
	if _, err := st.CreateWebhook("https://hooks.example/site", []string{model.WebhookUserSubRotated,
		model.WebhookUserTelegramLinked, model.WebhookUserTelegramUnlinked}, true); err != nil {
		t.Fatal(err)
	}
	drain := func() []string {
		t.Helper()
		var out []string
		for _, job := range takeWebhooks(t, st) {
			var p struct {
				Event string         `json:"event"`
				Data  map[string]any `json:"data"`
			}
			if err := json.Unmarshal(job.Body, &p); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(p.Data)
			out = append(out, p.Event+" "+string(b))
		}
		return out
	}
	ctx := context.Background()
	a, _ := st.CreateUser("ann", "uuid-ann", "pw", "tok-ann", 0, 0, 0)
	b, _ := st.CreateUser("bob", "uuid-bob", "pw", "tok-bob", 0, 0, 0)

	nu, err := m.RotateSubToken(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := drain()
	if len(got) != 1 || !strings.HasPrefix(got[0], model.WebhookUserSubRotated) ||
		!strings.Contains(got[0], `"sub_url":"https://`) || !strings.Contains(got[0], nu.SubToken) {
		t.Fatalf("rotation: %q", got)
	}

	if err := m.LinkUserTelegram(ctx, a.ID, 4242); err != nil {
		t.Fatal(err)
	}
	if got := drain(); len(got) != 1 || !strings.HasPrefix(got[0], model.WebhookUserTelegramLinked) ||
		!strings.Contains(got[0], `"telegram_id":4242`) {
		t.Fatalf("link: %q", got)
	}

	// The bot moves the chat to another account: the one it left is told about too.
	if err := st.SetUserTelegramChat(b.ID, 4242); err != nil {
		t.Fatal(err)
	}
	m.AuditTelegramDetached(ctx, a.ID, 4242)
	m.AuditTelegramLinked(ctx, b.ID, "bob")
	got = drain()
	if len(got) != 2 || !strings.HasPrefix(got[0], model.WebhookUserTelegramUnlinked) ||
		!strings.Contains(got[0], `"name":"ann"`) || !strings.HasPrefix(got[1], model.WebhookUserTelegramLinked) ||
		!strings.Contains(got[1], `"name":"bob"`) || !strings.Contains(got[1], `"telegram_id":4242`) {
		t.Fatalf("move: %q", got)
	}

	if err := m.UnlinkUserTelegram(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := drain(); len(got) != 1 || !strings.HasPrefix(got[0], model.WebhookUserTelegramUnlinked) ||
		!strings.Contains(got[0], `"telegram_id":4242`) {
		t.Fatalf("unlink: %q", got)
	}
	// Nothing linked: nothing to tell.
	if err := m.UnlinkUserTelegram(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := drain(); len(got) != 0 {
		t.Fatalf("unlinking an unlinked account: %q", got)
	}
}
