package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A redelivered webhook must not top the balance up twice.
func TestConfirmTopupOnce(t *testing.T) {
	t.Parallel()
	st, u, _, _, _ := planWriteFixture(t)
	now := time.Now().Unix()
	order, err := st.CreateOrder(OrderDraft{UserID: u.ID, Kind: model.OrderTopup, AmountRub: 300}, now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	spec := ConfirmSpec{CreditKop: 30000, Now: now}
	for i := range 2 {
		res, err := st.ConfirmOrder(order.ID, now, spec)
		if err != nil {
			t.Fatalf("confirm %d: %v", i, err)
		}
		if res.Claimed != (i == 0) {
			t.Fatalf("confirm %d claimed = %v", i, res.Claimed)
		}
	}
	w, _ := st.GetWallet(u.ID)
	if w.BalanceKop != 30000 {
		t.Fatalf("balance = %d, want 30000", w.BalanceKop)
	}
	txs, _ := st.ListBalanceTx(u.ID, 10)
	if len(txs) != 1 || txs[0].OrderID != order.ID || txs[0].BalanceKop != 30000 {
		t.Fatalf("ledger = %+v", txs)
	}
	got, _ := st.GetPaymentOrder(order.ID)
	if got.PlanName != "" || got.Kind != model.OrderTopup {
		t.Fatalf("topup order read back as %+v", got)
	}
}

// A code with one use left, redeemed by many users at once, goes to exactly one.
func TestPromoLastUseRace(t *testing.T) {
	t.Parallel()
	st, _, _, _, _ := planWriteFixture(t)
	p := &model.PromoCode{Code: "ONE", Kind: model.PromoBalance, Value: 10, MaxUses: 1, Enabled: true}
	if err := st.SavePromo(p, 1); err != nil {
		t.Fatalf("save: %v", err)
	}
	var ids []int64
	for _, n := range []string{"a", "b", "c", "d"} {
		u, err := st.CreateUser("race-"+n, "uuid-race-"+n, "pw", "tok-race-"+n, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := st.RedeemPromoBalance(p.ID, id, 1000, time.Now().Unix())
			if err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			} else if !errors.Is(err, ErrPromoUnavailable) {
				t.Errorf("redeem: %v", err)
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d users got the last use", won)
	}
}

// Deleting a user takes their ledger with them and unhooks whoever they invited.
func TestDeleteUserForgetsWallet(t *testing.T) {
	t.Parallel()
	st, ref, _, _, _ := planWriteFixture(t)
	friend, err := st.CreateUser("friend", "uuid-friend", "pw", "tok-friend", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSubscriberRef(42, ref.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got, err := st.AttachReferrerFromChat(friend.ID, 42); err != nil || got != ref.ID {
		t.Fatalf("attach = %d, %v", got, err)
	}
	if _, err := st.AdjustBalance(ref.ID, 500, "", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(ref.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if w, _ := st.GetWallet(friend.ID); w.ReferrerID != 0 {
		t.Fatalf("invitee still points at the deleted referrer %d", w.ReferrerID)
	}
	if txs, _ := st.ListBalanceTx(ref.ID, 10); len(txs) != 0 {
		t.Fatalf("ledger survived the user: %+v", txs)
	}
}

// Nobody refers themselves, and a referrer is set once.
func TestAttachReferrerGuards(t *testing.T) {
	t.Parallel()
	st, u, _, _, _ := planWriteFixture(t)
	if err := st.SetSubscriberRef(7, u.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.AttachReferrerFromChat(u.ID, 7); got != 0 {
		t.Fatal("a user became their own referrer")
	}
	other, _ := st.CreateUser("other", "uuid-other", "pw", "tok-other", 0, 0, 0)
	if err := st.SetSubscriberRef(7, other.ID, 1); err != nil {
		t.Fatal(err)
	}
	var ref int64
	_ = st.db.QueryRow(`SELECT ref_user_id FROM tg_subscribers WHERE chat_id = 7`).Scan(&ref)
	if ref != u.ID {
		t.Fatalf("a later invite replaced the first (%d)", ref)
	}
}
