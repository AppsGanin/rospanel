package core

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/payments"
	"github.com/AppsGanin/rospanel/internal/store"
)

// TestConfirmProviderOrderIdempotent is the headline payment-safety check: a
// re-delivered webhook (or an overlapping status poll) for an already-paid order
// must be a no-op — it must NOT apply the plan a second time and stack another
// paid period onto the user's expiry.
func TestConfirmProviderOrderIdempotent(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pay.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	u, err := st.CreateUser("buyer", "uuid-buyer", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	plan := &model.TariffPlan{Slug: "m1", Name: "Месяц", PriceRub: 100, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	order, err := st.CreatePaymentOrder(u.ID, plan.ID, plan.PriceRub)
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := st.SetPaymentOrderProvider(order.ID, payments.ProviderCryptoBot, "inv-1", "https://t.me/x"); err != nil {
		t.Fatalf("set provider: %v", err)
	}

	// The provider reports exactly the 100 ₽ the order was created for.
	paid := payments.Result{Status: payments.StatusPaid, AmountKopecks: 10000, Currency: "RUB"}

	// First confirmation: applies the plan and marks the order paid.
	if err := m.confirmProviderOrder(payments.ProviderCryptoBot, "inv-1", paid); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	after1, _ := st.GetUser(u.ID)
	o1, _ := st.GetPaymentOrder(order.ID)
	if o1.Status != "paid" {
		t.Fatalf("order status = %q, want paid", o1.Status)
	}
	if after1.ExpireAt == 0 {
		t.Fatal("plan not applied — expiry still unset after first confirm")
	}
	wantExpire := time.Now().Unix() + 30*86400
	if d := after1.ExpireAt - wantExpire; d < -5 || d > 5 {
		t.Fatalf("expiry = %d, want ~%d (now + 30d)", after1.ExpireAt, wantExpire)
	}

	// Second confirmation of the SAME order: must be a no-op (status already paid).
	if err := m.confirmProviderOrder(payments.ProviderCryptoBot, "inv-1", paid); err != nil {
		t.Fatalf("second confirm returned error: %v", err)
	}
	after2, _ := st.GetUser(u.ID)
	if after2.ExpireAt != after1.ExpireAt {
		t.Fatalf("expiry changed on redelivery: %d → %d (double-applied!)", after1.ExpireAt, after2.ExpireAt)
	}
}

// TestConfirmProviderOrderUnknown ensures a webhook for an unknown external id
// fails cleanly (looked-up order not found) rather than mutating anything.
func TestConfirmProviderOrderUnknown(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pay2.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}
	paid := payments.Result{Status: payments.StatusPaid, AmountKopecks: 10000, Currency: "RUB"}
	if err := m.confirmProviderOrder(payments.ProviderYooKassa, "does-not-exist", paid); err == nil {
		t.Fatal("expected error for unknown provider id")
	}
}

// TestConfirmProviderOrderAmountMismatch: a callback that reports a different
// amount (or currency) than the order was created for must NOT grant the plan.
// Amounts are fixed server-side, so this can only be a tampered/misrouted call.
func TestConfirmProviderOrderAmountMismatch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pay3.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st}

	u, err := st.CreateUser("buyer", "uuid-buyer", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	plan := &model.TariffPlan{Slug: "m1", Name: "Месяц", PriceRub: 100, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}

	newOrder := func(extID string) int64 {
		o, err := st.CreatePaymentOrder(u.ID, plan.ID, plan.PriceRub)
		if err != nil {
			t.Fatalf("create order: %v", err)
		}
		if err := st.SetPaymentOrderProvider(o.ID, payments.ProviderCryptoBot, extID, "https://t.me/x"); err != nil {
			t.Fatalf("set provider: %v", err)
		}
		return o.ID
	}

	cases := []struct {
		name string
		paid payments.Result
	}{
		{"underpaid", payments.Result{Status: payments.StatusPaid, AmountKopecks: 100, Currency: "RUB"}}, // 1 ₽ for a 100 ₽ plan
		{"wrong currency", payments.Result{Status: payments.StatusPaid, AmountKopecks: 10000, Currency: "USD"}},
	}
	for i, tc := range cases {
		extID := fmt.Sprintf("inv-mismatch-%d", i)
		orderID := newOrder(extID)
		if err := m.confirmProviderOrder(payments.ProviderCryptoBot, extID, tc.paid); err == nil {
			t.Fatalf("%s: confirm accepted a mismatched charge", tc.name)
		}
		o, _ := st.GetPaymentOrder(orderID)
		if o.Status == "paid" {
			t.Fatalf("%s: order marked paid despite mismatch", tc.name)
		}
		after, _ := st.GetUser(u.ID)
		if after.ExpireAt != 0 {
			t.Fatalf("%s: plan applied despite mismatch (expiry=%d)", tc.name, after.ExpireAt)
		}
	}

	// Control: an unknown amount (provider reported none) must still fail OPEN so a
	// response-format change never blocks a real payment.
	extID := "inv-unknown-amount"
	newOrder(extID)
	unknown := payments.Result{Status: payments.StatusPaid}
	if err := m.confirmProviderOrder(payments.ProviderCryptoBot, extID, unknown); err != nil {
		t.Fatalf("unknown amount must fail open, got: %v", err)
	}
	if after, _ := st.GetUser(u.ID); after.ExpireAt == 0 {
		t.Fatal("unknown amount: plan was not applied (should fail open)")
	}
}
