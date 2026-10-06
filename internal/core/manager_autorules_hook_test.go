package core

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// With a webhook on user.auto_message a rule reaches the accounts the bot cannot —
// no Telegram, a blocked bot — through it, copies what the bot delivered, leaves out
// who turned mailings off, writes once per cycle whichever way, and with the bot off
// covers everyone.
func TestAutoRuleWebhook(t *testing.T) {
	t.Parallel()
	m, st, sent := newRulesManager(t)
	m.webhookCh = make(chan webhookJob, 1)
	now := time.Now()
	created := now.Add(-2 * time.Hour)
	userWithChat(t, st, "tg", 301, created)
	userWithChat(t, st, "blocked", 302, created)
	userWithChat(t, st, "optout", 303, created)
	if err := st.ExecForTest(`UPDATE tg_subscribers SET active = 0 WHERE chat_id = 302`); err != nil {
		t.Fatal(err)
	}
	if err := st.ExecForTest(`UPDATE tg_subscribers SET opt_out = 1 WHERE chat_id = 303`); err != nil {
		t.Fatal(err)
	}
	webUser := func(name string) int64 {
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.BackdateUserForTest(u.ID, created); err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	web := webUser("web")
	if err := st.SetUserExternalID(web, "site-7"); err != nil {
		t.Fatal(err)
	}
	r := &model.AutoRule{Name: "Помочь подключиться", Enabled: true, Trigger: model.TriggerNoConnect,
		DelayHours: 1, Text: "Привет, {name}! Код {code}", DiscountPercent: 15, DiscountDays: 5}
	if err := m.SaveAutoRule(r); err != nil {
		t.Fatal(err)
	}
	hooks := func() map[string]map[string]any {
		t.Helper()
		out := map[string]map[string]any{}
		for _, j := range takeWebhooks(t, st) {
			var p struct {
				Event string         `json:"event"`
				Data  map[string]any `json:"data"`
			}
			if err := json.Unmarshal(j.Body, &p); err != nil {
				t.Fatal(err)
			}
			if p.Event != model.WebhookUserAutoMessage {
				t.Fatalf("unexpected event %s", p.Event)
			}
			out[p.Data["name"].(string)] = p.Data
		}
		return out
	}
	names := func(h map[string]map[string]any) []string {
		var out []string
		for n := range h {
			out = append(out, n)
		}
		sort.Strings(out)
		return out
	}

	// No webhook: the bot alone, as before.
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 1 || got[0].chat != 301 {
		t.Fatalf("bot sends = %+v", got)
	}
	if h := hooks(); len(h) != 0 {
		t.Fatalf("webhooks with none subscribed: %v", names(h))
	}

	if _, err := st.CreateWebhook("https://hooks.example/x", []string{model.WebhookUserAutoMessage}, true); err != nil {
		t.Fatal(err)
	}
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 0 {
		t.Fatalf("the bot wrote again: %+v", got)
	}
	h := hooks()
	if got := names(h); len(got) != 2 || got[0] != "blocked" || got[1] != "web" {
		t.Fatalf("external targets = %v, want blocked and web", got)
	}
	w := h["web"]
	if w["telegram_sent"] != false || w["external_id"] != "site-7" || w["trigger"] != model.TriggerNoConnect ||
		w["text"] != "Привет, web! Код "+w["code"].(string) || w["percent"] != float64(15) {
		t.Fatalf("user.auto_message = %v", w)
	}
	if wal, _ := st.GetWalletLite(web); wal.PromoID == 0 {
		t.Fatal("the code was not attached to the web user's next payment")
	}
	if h["blocked"]["telegram_id"] != float64(302) {
		t.Fatalf("blocked = %v", h["blocked"])
	}
	m.RunAutoRules(now.Unix())
	if h := hooks(); len(h) != 0 {
		t.Fatalf("a second sweep repeated: %v", names(h))
	}
	if rules, _ := m.ListAutoRules(); rules[0].Stats.Sent != 3 {
		t.Fatalf("stats = %+v", rules[0].Stats)
	}

	// The bot delivers, the external system gets a copy.
	userWithChat(t, st, "tg2", 304, created)
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 1 || got[0].chat != 304 {
		t.Fatalf("bot sends = %+v", got)
	}
	if h := hooks(); len(h) != 1 || h["tg2"]["telegram_sent"] != true {
		t.Fatalf("copy of a bot message = %v", h)
	}

	// The bot off: every account through the external system — once, as ever, and
	// still not who opted out.
	if err := st.ExecForTest(`UPDATE settings SET tg_user_bot_enabled = 0`); err != nil {
		t.Fatal(err)
	}
	userWithChat(t, st, "tg3", 305, created)
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 0 {
		t.Fatalf("the bot wrote while off: %+v", got)
	}
	if got := names(hooks()); len(got) != 1 || got[0] != "tg3" {
		t.Fatalf("with the bot off = %v, want tg3 only", got)
	}
}

// A Telegram that moved to another account carries none of the first one's sends: the
// new account hears the rule too, once.
func TestAutoRuleChatMovedAccounts(t *testing.T) {
	t.Parallel()
	m, st, sent := newRulesManager(t)
	now := time.Now()
	first := userWithChat(t, st, "first", 601, now.Add(-2*time.Hour))
	r := &model.AutoRule{Name: "Помочь", Enabled: true, Trigger: model.TriggerNoConnect, DelayHours: 1, Text: "Привет, {name}"}
	if err := m.SaveAutoRule(r); err != nil {
		t.Fatal(err)
	}
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 1 || got[0].chat != 601 {
		t.Fatalf("first = %+v", got)
	}
	if err := st.SetUserTelegramChat(first, 0); err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateUser("second", "uuid-second", "pw", "tok-second", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTelegramChat(second.ID, 601); err != nil {
		t.Fatal(err)
	}
	if err := st.BackdateUserForTest(second.ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 1 || got[0].chat != 601 || !strings.Contains(got[0].text, "second") {
		t.Fatalf("after the move = %+v", got)
	}
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 0 {
		t.Fatalf("repeated: %+v", got)
	}
}
