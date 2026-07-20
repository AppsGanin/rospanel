package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// planWriteFixture builds a store with one user, one plan and one pending order,
// plus the plan write that order pays for.
func planWriteFixture(t *testing.T) (*Store, *model.User, *model.TariffPlan, *model.PaymentOrder, UserPlanWrite) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "pay.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
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
	now := time.Now().Unix()
	return st, u, plan, order, UserPlanWrite{
		UserID:      u.ID,
		DataLimit:   10 << 30,
		ExpireAt:    now + 30*86400,
		DeviceLimit: 3,
		ResetPeriod: "none",
		ResetAnchor: now,
		PlanID:      plan.ID,
	}
}

// failUserWrites installs a trigger that aborts any UPDATE on users, and returns a
// func that removes it. This is how the test reproduces a crash landing between the
// order's paid claim and the plan it pays for: the claim's statement succeeds, the
// user's does not.
func failUserWrites(t *testing.T, st *Store) func() {
	t.Helper()
	if _, err := st.db.Exec(
		`CREATE TRIGGER t_fail_users BEFORE UPDATE ON users
		 BEGIN SELECT RAISE(ABORT, 'simulated crash'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}
	return func() {
		if _, err := st.db.Exec(`DROP TRIGGER t_fail_users`); err != nil {
			t.Fatalf("drop trigger: %v", err)
		}
	}
}

// TestConfirmPaymentOrderAtomic is the money-safety check. If the plan write fails
// after the order has been claimed, the claim must NOT survive: an order left
// 'paid' with no plan granted is invisible to every retry path in the panel (they
// all select status = 'pending'), so it would mean money taken and nothing
// delivered, forever.
func TestConfirmPaymentOrderAtomic(t *testing.T) {
	st, u, plan, order, w := planWriteFixture(t)
	defer st.Close()

	restore := failUserWrites(t, st)
	claimed, err := st.ConfirmPaymentOrder(order.ID, time.Now().Unix(), w)
	if err == nil {
		t.Fatal("expected the plan write to fail, got nil error")
	}
	if claimed {
		t.Fatal("claimed must be false when the transaction rolled back")
	}

	// The invariant: still pending, so the 25s fallback poll re-confirms it.
	got, err := st.GetPaymentOrder(order.ID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if got.Status != "pending" {
		t.Fatalf("order status = %q after a failed confirm, want \"pending\" — "+
			"a paid order whose plan was never applied is unrecoverable", got.Status)
	}
	if got.PaidAt != 0 {
		t.Errorf("paid_at = %d after a failed confirm, want 0", got.PaidAt)
	}
	// And the user got nothing, so the retry starts from a clean slate.
	cur, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if cur.PlanID != 0 || cur.ExpireAt != 0 || cur.DataLimit != 0 {
		t.Fatalf("user partially updated by a rolled-back confirm: %+v", cur)
	}

	// Same call once the write can land: both halves commit together.
	restore()
	claimed, err = st.ConfirmPaymentOrder(order.ID, time.Now().Unix(), w)
	if err != nil {
		t.Fatalf("retry confirm: %v", err)
	}
	if !claimed {
		t.Fatal("retry did not win the claim")
	}
	got, _ = st.GetPaymentOrder(order.ID)
	if got.Status != "paid" || got.PaidAt == 0 {
		t.Fatalf("order not paid after a successful confirm: %+v", got)
	}
	cur, _ = st.GetUser(u.ID)
	if cur.PlanID != plan.ID || cur.ExpireAt != w.ExpireAt || cur.DataLimit != w.DataLimit {
		t.Fatalf("plan not applied: plan=%d expire=%d limit=%d", cur.PlanID, cur.ExpireAt, cur.DataLimit)
	}
	if cur.DeviceLimit != w.DeviceLimit {
		t.Errorf("device limit = %d, want %d", cur.DeviceLimit, w.DeviceLimit)
	}
}

// TestConfirmPaymentOrderClaimsOnce covers the concurrency half the CAS was always
// there for: a re-delivered webhook overlapping the status poll must apply the plan
// once, not stack a second period onto the expiry.
func TestConfirmPaymentOrderClaimsOnce(t *testing.T) {
	st, u, _, order, w := planWriteFixture(t)
	defer st.Close()

	claimed, err := st.ConfirmPaymentOrder(order.ID, time.Now().Unix(), w)
	if err != nil || !claimed {
		t.Fatalf("first confirm: claimed=%v err=%v", claimed, err)
	}
	first, _ := st.GetUser(u.ID)

	// Second confirmer arrives with a later expiry, as a stacked renewal would.
	w2 := w
	w2.ExpireAt = w.ExpireAt + 30*86400
	claimed, err = st.ConfirmPaymentOrder(order.ID, time.Now().Unix(), w2)
	if err != nil {
		t.Fatalf("second confirm: %v", err)
	}
	if claimed {
		t.Fatal("second confirmer won the claim — one payment would extend the user twice")
	}
	after, _ := st.GetUser(u.ID)
	if after.ExpireAt != first.ExpireAt {
		t.Fatalf("expiry moved on a losing confirm: %d -> %d", first.ExpireAt, after.ExpireAt)
	}
}

// TestApplyUserPlanAtomic covers the same all-or-nothing guarantee on the plain
// assignment path (manual plan change, trial grant, free-plan downgrade): a user
// left with new limits but the old plan_id is a state nothing reconciles.
func TestApplyUserPlanAtomic(t *testing.T) {
	st, u, _, _, w := planWriteFixture(t)
	defer st.Close()

	restore := failUserWrites(t, st)
	if err := st.ApplyUserPlan(w); err == nil {
		t.Fatal("expected ApplyUserPlan to fail")
	}
	restore()

	cur, _ := st.GetUser(u.ID)
	if cur.PlanID != 0 || cur.DataLimit != 0 || cur.ExpireAt != 0 {
		t.Fatalf("partial plan write survived the rollback: %+v", cur)
	}

	if err := st.ApplyUserPlan(w); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cur, _ = st.GetUser(u.ID)
	if cur.PlanID != w.PlanID || cur.DataLimit != w.DataLimit || cur.ExpireAt != w.ExpireAt {
		t.Fatalf("plan not applied in full: %+v", cur)
	}
}
