package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/payments"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Every provider callback lands in the journal with what came of it: a paid order,
// its repeat, a forged signature, an unknown invoice.
func TestProviderWebhookJournal(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "hooks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &Manager{store: st}
	const token = "12345:secret"
	if err := st.SavePaymentProvider(model.PaymentProvider{
		Key: payments.ProviderCryptoBot, Enabled: true, Config: map[string]string{"token": token},
	}); err != nil {
		t.Fatal(err)
	}
	u, _ := st.CreateUser("buyer", "uuid-buyer", "pw", "tok", 0, 0, 0)
	plan := &model.TariffPlan{Slug: "m1", Name: "Месяц", PriceRub: 100, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	order, _ := st.CreatePaymentOrder(u.ID, plan.ID, plan.PriceRub)
	if err := st.SetPaymentOrderProvider(order.ID, payments.ProviderCryptoBot, "777", "https://t.me/x"); err != nil {
		t.Fatal(err)
	}
	sign := func(body string) http.Header {
		secret := sha256.Sum256([]byte(token))
		mac := hmac.New(sha256.New, secret[:])
		mac.Write([]byte(body))
		h := http.Header{}
		h.Set("Crypto-Pay-Api-Signature", hex.EncodeToString(mac.Sum(nil)))
		h.Set("Cookie", "session=must-not-be-kept")
		h.Set("X-Secret", "must-not-be-kept-either")
		return h
	}
	paid := `{"update_type":"invoice_paid","payload":{"invoice_id":777,"status":"paid","amount":"100","fiat":"RUB"}}`
	unknown := `{"update_type":"invoice_paid","payload":{"invoice_id":999,"status":"paid","amount":"100","fiat":"RUB"}}`

	_ = m.HandleProviderWebhook(payments.ProviderCryptoBot, []byte(paid), sign(paid), "1.2.3.4")
	_ = m.HandleProviderWebhook(payments.ProviderCryptoBot, []byte(paid), sign(paid), "1.2.3.4")
	_ = m.HandleProviderWebhook(payments.ProviderCryptoBot, []byte(paid), http.Header{}, "5.6.7.8")
	_ = m.HandleProviderWebhook(payments.ProviderCryptoBot, []byte(unknown), sign(unknown), "1.2.3.4")

	got, err := m.PaymentWebhooks(store.PaymentWebhookFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{model.WebhookOutcomeNoOrder, model.WebhookOutcomeRejected, model.WebhookOutcomeDuplicate, model.WebhookOutcomePaid}
	if len(got) != len(want) {
		t.Fatalf("journal has %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Outcome != w {
			t.Errorf("row %d outcome = %q, want %q", i, got[i].Outcome, w)
		}
	}
	if got[3].OrderID != order.ID || got[3].ProviderID != "777" || got[3].RemoteIP != "1.2.3.4" {
		t.Errorf("paid row = %+v", got[3])
	}
	if got[1].Error == "" {
		t.Error("a rejected callback carries no error text")
	}
	for _, r := range got {
		if strings.Contains(r.Headers, "must-not-be-kept") || !strings.Contains(r.Headers, "Crypto-Pay-Api-Signature") && r.Outcome == model.WebhookOutcomePaid {
			t.Fatalf("cookie kept in the journal: %q", r.Headers)
		}
	}
	failed, _ := m.PaymentWebhooks(store.PaymentWebhookFilter{Failed: true})
	if len(failed) != 2 {
		t.Fatalf("failed filter returned %d rows, want 2", len(failed))
	}
	if o, _ := st.GetPaymentOrder(order.ID); o.Status != "paid" {
		t.Fatalf("order status = %q", o.Status)
	}
}
