package core

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

type sentMsg struct {
	chat int64
	text string
}

func newRulesManager(t *testing.T) (*Manager, *store.Store, func() []sentMsg) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "rules.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.ExecForTest(`UPDATE settings SET tg_user_bot_enabled = 1, billing_enabled = 1`); err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: st}
	var mu sync.Mutex
	var sent []sentMsg
	m.SetUserMessenger(func(chat int64, html string, _ []model.BroadcastButton) {
		mu.Lock()
		sent = append(sent, sentMsg{chat, html})
		mu.Unlock()
	})
	return m, st, func() []sentMsg {
		mu.Lock()
		defer mu.Unlock()
		out := sent
		sent = nil
		return out
	}
}

// userWithChat makes a user linked to a chat that takes messages.
func userWithChat(t *testing.T, st *store.Store, name string, chat int64, created time.Time) int64 {
	t.Helper()
	u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTelegramChat(u.ID, chat); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSubscriber(chat, u.ID, "", name, "ru", created.Unix()); err != nil {
		t.Fatal(err)
	}
	if err := st.BackdateUserForTest(u.ID, created); err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// A rule writes once to each person who reached its trigger past the delay, not to
// those whose trigger is too fresh or too old, and a discount rule attaches a code.
func TestAutoRuleNoConnect(t *testing.T) {
	t.Parallel()
	m, st, sent := newRulesManager(t)
	now := time.Now()
	due := userWithChat(t, st, "due", 101, now.Add(-2*time.Hour))
	userWithChat(t, st, "fresh", 102, now.Add(-10*time.Minute))
	userWithChat(t, st, "ancient", 103, now.Add(-30*24*time.Hour))
	active := userWithChat(t, st, "active", 104, now.Add(-2*time.Hour))
	if err := st.ExecForTest(`UPDATE users SET last_seen = ? WHERE id = ?`, now.Unix(), active); err != nil {
		t.Fatal(err)
	}
	optOut := userWithChat(t, st, "optout", 105, now.Add(-2*time.Hour))
	_ = optOut
	if err := st.ExecForTest(`UPDATE tg_subscribers SET opt_out = 1 WHERE chat_id = 105`); err != nil {
		t.Fatal(err)
	}
	r := &model.AutoRule{Name: "Помочь подключиться", Enabled: true, Trigger: model.TriggerNoConnect,
		DelayHours: 1, Text: "Привет, {name}! Код {code} на {percent}%", DiscountPercent: 20, DiscountDays: 5}
	if err := m.SaveAutoRule(r); err != nil {
		t.Fatal(err)
	}
	m.RunAutoRules(now.Unix())
	got := sent()
	if len(got) != 1 || got[0].chat != 101 || !strings.Contains(got[0].text, "Привет, due!") || strings.Contains(got[0].text, "{code}") {
		t.Fatalf("sent = %+v", got)
	}
	w, _ := st.GetWalletLite(due)
	if w.PromoID == 0 {
		t.Fatal("the discount code was not attached to the next payment")
	}
	m.RunAutoRules(now.Unix())
	if again := sent(); len(again) != 0 {
		t.Fatalf("the rule wrote twice: %+v", again)
	}
	rules, _ := m.ListAutoRules()
	if rules[0].Stats.Sent != 1 {
		t.Fatalf("stats = %+v", rules[0].Stats)
	}
	// Rule codes are not win-back codes.
	if wb, _ := m.WinbackStats(); wb.Sent != 0 {
		t.Fatalf("win-back stats count rule codes: %+v", wb)
	}
}

// An idle spell and a lapsed term repeat; opening the bot without registering
// happens once.
func TestAutoRuleCycles(t *testing.T) {
	t.Parallel()
	m, st, sent := newRulesManager(t)
	now := time.Now()
	idle := userWithChat(t, st, "idle", 201, now.Add(-60*24*time.Hour))
	if err := st.ExecForTest(`UPDATE users SET last_seen = ?, expire_at = ? WHERE id = ?`,
		now.Add(-3*24*time.Hour).Unix(), now.Add(10*24*time.Hour).Unix(), idle); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSubscriber(300, 0, "", "guest", "ru", now.Add(-5*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*model.AutoRule{
		{Name: "idle", Enabled: true, Trigger: model.TriggerIdle, DelayHours: 48, Text: "Скучаем"},
		{Name: "signup", Enabled: true, Trigger: model.TriggerNoSignup, DelayHours: 1, Text: "Зарегистрируйтесь"},
	} {
		if err := m.SaveAutoRule(r); err != nil {
			t.Fatal(err)
		}
	}
	m.RunAutoRules(now.Unix())
	chats := map[int64]bool{}
	for _, s := range sent() {
		chats[s.chat] = true
	}
	if !chats[201] || !chats[300] || len(chats) != 2 {
		t.Fatalf("first sweep wrote to %v", chats)
	}
	// Came back, then went quiet again: a new spell, a new message.
	if err := st.ExecForTest(`UPDATE users SET last_seen = ? WHERE id = ?`, now.Add(-50*time.Hour).Unix(), idle); err != nil {
		t.Fatal(err)
	}
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 1 || got[0].chat != 201 {
		t.Fatalf("second spell = %+v", got)
	}
	if err := m.SaveAutoRule(&model.AutoRule{Name: "x", Trigger: model.TriggerNoSignup, DelayHours: 1,
		Text: "x", DiscountPercent: 10, DiscountDays: 3}); err == nil {
		t.Fatal("a discount for a chat with no account was accepted")
	}
}

// A paid term that ran out gets the lapsed message, once per term.
func TestAutoRuleLapsed(t *testing.T) {
	t.Parallel()
	m, st, sent := newRulesManager(t)
	now := time.Now()
	gone := userWithChat(t, st, "gone", 401, now.Add(-90*24*time.Hour))
	never := userWithChat(t, st, "never", 402, now.Add(-90*24*time.Hour))
	for _, id := range []int64{gone, never} {
		if err := st.ExecForTest(`UPDATE users SET expire_at = ? WHERE id = ?`, now.Add(-2*24*time.Hour).Unix(), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ExecForTest(`INSERT INTO payment_orders (user_id, plan_id, amount_rub, status, created_at, paid_at) VALUES (?, 1, 100, 'paid', ?, ?)`,
		gone, now.Add(-32*24*time.Hour).Unix(), now.Add(-32*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutoRule(&model.AutoRule{Name: "back", Enabled: true, Trigger: model.TriggerLapsed, DelayHours: 24, Text: "Вернитесь"}); err != nil {
		t.Fatal(err)
	}
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 1 || got[0].chat != 401 {
		t.Fatalf("lapsed = %+v", got)
	}
	m.RunAutoRules(now.Unix())
	if got := sent(); len(got) != 0 {
		t.Fatalf("lapsed twice = %+v", got)
	}
}
