package core

import (
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

	// First confirmation: applies the plan and marks the order paid.
	if err := m.confirmProviderOrder(payments.ProviderCryptoBot, "inv-1"); err != nil {
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
	if err := m.confirmProviderOrder(payments.ProviderCryptoBot, "inv-1"); err != nil {
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
	if err := m.confirmProviderOrder(payments.ProviderYooKassa, "does-not-exist"); err == nil {
		t.Fatal("expected error for unknown provider id")
	}
}
