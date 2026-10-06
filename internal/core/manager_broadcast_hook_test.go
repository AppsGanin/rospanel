package core

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// With a webhook on broadcast.sent a broadcast reaches the accounts the bot does not —
// all of them with the bot off, minus who turned mailings off — and one with nothing
// left for the bot is done at once.
func TestBroadcastWebhook(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	m.webhookCh = make(chan webhookJob, 1)
	st := m.store
	ctx := context.Background()
	mk := func(name string, chat int64) int64 {
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if chat != 0 {
			if err := st.SetUserTelegramChat(u.ID, chat); err != nil {
				t.Fatal(err)
			}
			subscribeChat(t, m, chat, u.ID)
		}
		return u.ID
	}
	web := mk("web", 0)
	if err := st.SetUserExternalID(web, "site-1"); err != nil {
		t.Fatal(err)
	}
	tg := mk("tg", 402)
	// Approved by an operator: the account holds the chat, the bot's own row still
	// says nobody — the bot reaches it all the same.
	approved := mk("approved", 0)
	if err := st.SetUserTelegramChat(approved, 403); err != nil {
		t.Fatal(err)
	}
	subscribeChat(t, m, 403, 0)
	mk("opt", 401)
	if err := st.SetSubscriberOptOut(401, true, 1); err != nil {
		t.Fatal(err)
	}
	sent := func() (map[string]any, []int64) {
		t.Helper()
		jobs := takeWebhooks(t, st)
		if len(jobs) != 1 {
			t.Fatalf("%d broadcast.sent deliveries, want 1", len(jobs))
		}
		var p struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(jobs[0].Body, &p); err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, u := range p.Data["users"].([]any) {
			ids = append(ids, int64(u.(map[string]any)["id"].(float64)))
		}
		return p.Data, ids
	}
	if _, err := st.CreateWebhook("https://hooks.example/b", []string{model.WebhookBroadcastSent}, true); err != nil {
		t.Fatal(err)
	}

	// The bot on: it takes tg, the external system the account without Telegram.
	b, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: "<b>Новости</b>", Audience: model.AudienceAll})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartBroadcast(b.ID); err != nil {
		t.Fatal(err)
	}
	d, ids := sent()
	if len(ids) != 1 || ids[0] != web || d["text"] != "<b>Новости</b>" || d["telegram_recipients"] != float64(2) ||
		d["users"].([]any)[0].(map[string]any)["external_id"] != "site-1" {
		t.Fatalf("broadcast.sent = %v", d)
	}
	if got, _ := m.GetBroadcast(b.ID); got.Status != model.BroadcastRunning || got.HookUsers != 1 {
		t.Fatalf("with the bot: status %s, hook users %d", got.Status, got.HookUsers)
	}

	// The bot off: every account but who opted out, and nothing left to run.
	if err := st.SetTelegramUserBot(false, "111:AAA", model.RegOpen, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: "x", MediaKind: "photo", MediaName: "a.png"}); err == nil ||
		!strings.Contains(err.Error(), "вложение") {
		t.Fatalf("an attachment with only the external system: %v", err)
	}
	b, err = m.CreateBroadcast(ctx, &model.Broadcast{Text: "без бота", Audience: model.AudienceAll})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartBroadcast(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, ids := sent(); len(ids) != 3 || !slices.Contains(ids, web) || !slices.Contains(ids, tg) || !slices.Contains(ids, approved) {
		t.Fatalf("with the bot off = %v, want web, tg and approved", ids)
	}
	if got, _ := m.GetBroadcast(b.ID); got.Status != model.BroadcastDone || got.Total != 0 {
		t.Fatalf("webhook-only broadcast: status %s, total %d", got.Status, got.Total)
	}
	if n, _ := m.AudienceHookPreview(model.AudienceAll); n != 3 {
		t.Fatalf("preview = %d, want 3", n)
	}

	// Neither the bot nor a webhook: refused as before.
	hooks, _ := st.ListWebhooks()
	for _, h := range hooks {
		if err := st.UpdateWebhook(h.ID, h.URL, h.Events, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: "x"}); err == nil {
		t.Fatal("created with no way to deliver it")
	}
}
