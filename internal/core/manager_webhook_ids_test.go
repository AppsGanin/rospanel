package core

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Every user payload names the user the way an external system knows them — its own
// id and the Telegram — and a payment event carries the user and the balance it left.
func TestWebhookPayloadIDs(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "ids.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st, tz: time.UTC, webhookCh: make(chan webhookJob, 1)}
	a, _ := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0)
	b, _ := st.CreateUser("b", "uuid-b", "pw", "tok-b", 0, 0, 0)
	if err := st.SetUserExternalID(a.ID, "site-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTelegramChat(b.ID, 777); err != nil {
		t.Fatal(err)
	}
	a, _ = st.GetUser(a.ID)
	b, _ = st.GetUser(b.ID)
	got := m.usersEventData([]model.User{*a, *b})
	if got[0]["external_id"] != "site-a" || got[0]["telegram_id"] != int64(0) ||
		got[1]["external_id"] != "" || got[1]["telegram_id"] != int64(777) {
		t.Fatalf("payloads = %v", got)
	}

	if _, err := st.CreateWebhook("https://hooks.example/p", []string{model.WebhookPaymentPaid}, true); err != nil {
		t.Fatal(err)
	}
	m.emitPaymentWebhook(model.WebhookPaymentPaid,
		&model.PaymentOrder{ID: 9, UserID: a.ID, AmountRub: 199, Status: "paid"}, map[string]any{"renewal": true})
	jobs := takeWebhooks(t, st)
	if len(jobs) != 1 {
		t.Fatalf("%d deliveries", len(jobs))
	}
	var p struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(jobs[0].Body, &p); err != nil {
		t.Fatal(err)
	}
	user, _ := p.Data["user"].(map[string]any)
	if p.Data["id"] != float64(9) || p.Data["amount_rub"] != float64(199) || p.Data["renewal"] != true ||
		p.Data["user_balance_kop"] != float64(0) || user["external_id"] != "site-a" {
		t.Fatalf("payment.paid = %s", jobs[0].Body)
	}
}
