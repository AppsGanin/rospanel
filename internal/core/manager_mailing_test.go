package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The mailing switch is one switch whoever flips it — the API, the operator, the bot —
// and every mailing honours it: broadcasts through the bot and the external system,
// automatic messages both ways. The account's language goes before its Telegram's.
func TestUserMailingAndLang(t *testing.T) {
	t.Parallel()
	m, st, sent := newRulesManager(t)
	m.webhookCh = make(chan webhookJob, 1)
	ctx := context.Background()
	now := time.Now()
	created := now.Add(-2 * time.Hour)
	tg := userWithChat(t, st, "tg", 801, created)
	webU, err := st.CreateUser("web", "uuid-web", "pw", "tok-web", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BackdateUserForTest(webU.ID, created); err != nil {
		t.Fatal(err)
	}
	web := webU.ID
	if _, err := st.CreateWebhook("https://hooks.example/m",
		[]string{model.WebhookUserMailing, model.WebhookUserAutoMessage, model.WebhookBroadcastSent}, true); err != nil {
		t.Fatal(err)
	}

	// The bot's switch reaches the account; the API's reaches the chat.
	if err := m.SetChatMailing(ctx, 801, false); err != nil {
		t.Fatal(err)
	}
	if on, _, _ := m.UserContact(tg); on {
		t.Fatal("the bot's switch did not reach the account")
	}
	if err := m.SetUserMailing(ctx, tg, true); err != nil {
		t.Fatal(err)
	}
	if off, _ := st.ChatMailingOff(801); off {
		t.Fatal("the API's switch did not reach the chat")
	}
	if err := m.SetUserMailing(ctx, web, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserMailing(ctx, web, false); err != nil { // no change: no event
		t.Fatal(err)
	}
	if err := m.SetChatMailing(ctx, 801, true); err != nil { // an old button: no change, no event
		t.Fatal(err)
	}
	var mailing []bool
	for _, j := range takeWebhooks(t, st) {
		if j.Event == model.WebhookUserMailing {
			var p struct {
				Data map[string]any `json:"data"`
			}
			mustJSON(t, j.Body, &p)
			mailing = append(mailing, p.Data["mailing"].(bool))
		}
	}
	if len(mailing) != 3 || mailing[0] || !mailing[1] || mailing[2] {
		t.Fatalf("user.mailing = %v, want off, on, off", mailing)
	}

	// Mailings skip the account that said no.
	r := &model.AutoRule{Name: "Помочь", Enabled: true, Trigger: model.TriggerNoConnect, DelayHours: 1, Text: "Привет"}
	if err := m.SaveAutoRule(r); err != nil {
		t.Fatal(err)
	}
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 1 || got[0].chat != 801 {
		t.Fatalf("bot sends = %+v", got)
	}
	for _, j := range takeWebhooks(t, st) {
		var p struct {
			Data map[string]any `json:"data"`
		}
		mustJSON(t, j.Body, &p)
		if p.Data["id"] == float64(web) {
			t.Fatalf("an automatic message went to an account with mailings off: %s", j.Body)
		}
	}
	users, err := m.audienceHookUsers(model.AudienceAll, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatalf("broadcast to the external system = %v, want nobody", users)
	}
	if err := m.SetUserMailing(ctx, tg, false); err != nil {
		t.Fatal(err)
	}
	if chats, _ := m.audienceChats(model.AudienceAll); len(chats) != 0 {
		t.Fatalf("the bot's broadcast = %v, want nobody", chats)
	}

	// The account's language over its Telegram's ("ru" from userWithChat).
	if got := m.userLang(801); got != i18n.RU {
		t.Fatalf("lang = %s, want the Telegram's ru", got)
	}
	if err := m.SetUserLang(ctx, tg, "en"); err != nil {
		t.Fatal(err)
	}
	if got := m.userLang(801); got != i18n.EN {
		t.Fatalf("lang = %s, want the account's en", got)
	}
	if _, lang, _ := m.UserContact(web); lang != "" {
		t.Fatalf("a web user with no language set = %q, want empty", lang)
	}
	if err := m.SetUserLang(ctx, web, "de"); err == nil {
		t.Fatal("de accepted")
	}
}

func mustJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}
