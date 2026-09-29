package core

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/payments"
	"github.com/AppsGanin/rospanel/internal/store"
)

// A Stars payment goes ahead only for our invoice, at the star count it asked for,
// and is applied once; one paid with a different count grants nothing.
func TestStarsPayment(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "stars.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &Manager{store: st}
	u, _ := st.CreateUser("buyer", "uuid-buyer", "pw", "tok", 0, 0, 0)
	plan := &model.TariffPlan{Slug: "m1", Name: "Месяц", PriceRub: 100, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	order, _ := st.CreatePaymentOrder(u.ID, plan.ID, plan.PriceRub)
	const payload = "o1-56"
	if err := st.SetPaymentOrderProvider(order.ID, payments.ProviderStars, payload, "https://t.me/$x"); err != nil {
		t.Fatal(err)
	}
	if err := m.StarsPreCheckout(payload, "XTR", 56); err != nil {
		t.Fatalf("pre-checkout refused: %v", err)
	}
	if err := m.StarsPreCheckout(payload, "XTR", 1); err == nil {
		t.Fatal("pre-checkout passed a different star count")
	}
	if err := m.StarsPreCheckout("o9-5", "XTR", 5); err == nil {
		t.Fatal("pre-checkout passed an unknown invoice")
	}
	if err := m.ConfirmStarsPayment(payload, "XTR", 1, []byte(`{}`)); err == nil {
		t.Fatal("a payment of the wrong count was applied")
	}
	if after, _ := st.GetUser(u.ID); after.ExpireAt != 0 {
		t.Fatal("the plan was granted on a mismatch")
	}
	if err := m.ConfirmStarsPayment(payload, "XTR", 56, []byte(`{"successful_payment":{}}`)); err != nil {
		t.Fatal(err)
	}
	first, _ := st.GetUser(u.ID)
	if first.ExpireAt == 0 {
		t.Fatal("the plan was not granted")
	}
	_ = m.ConfirmStarsPayment(payload, "XTR", 56, []byte(`{}`))
	if again, _ := st.GetUser(u.ID); again.ExpireAt != first.ExpireAt {
		t.Fatal("a repeated payment message extended the plan twice")
	}
	if err := m.StarsPreCheckout(payload, "XTR", 56); err == nil {
		t.Fatal("pre-checkout passed a paid order")
	}
	j, _ := m.PaymentWebhooks(store.PaymentWebhookFilter{})
	want := []string{model.WebhookOutcomeDuplicate, model.WebhookOutcomePaid, model.WebhookOutcomeMismatch}
	if len(j) != 3 {
		t.Fatalf("journal = %d rows", len(j))
	}
	for i, w := range want {
		if j[i].Outcome != w {
			t.Errorf("journal row %d = %q, want %q", i, j[i].Outcome, w)
		}
	}
}
