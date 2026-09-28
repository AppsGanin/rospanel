package core

import (
	"context"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// paidOrder buys the plan for u through a manual order and returns the order.
func paidOrder(t *testing.T, m *Manager, userID, planID int64) *model.PaymentOrder {
	t.Helper()
	o, _, err := m.RequestPlanPayment(context.Background(), i18n.RU, userID, planID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmPayment(context.Background(), o.ID); err != nil {
		t.Fatal(err)
	}
	got, err := m.store.GetPaymentOrder(o.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Money the payment system gave back leaves revenue, takes the plan's time with it,
// can no longer be refunded to the balance, and a repeated notice changes nothing.
func TestProviderRefundPlanOrder(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "chargeback")
	o := paidOrder(t, m, u.ID, plan.ID)
	m.providerRefunded(ctx, o)
	if got, _ := st.GetUser(u.ID); got.PlanID == plan.ID {
		t.Fatal("the plan survived the refund")
	}
	if sum, _ := st.PaidSumSince(0); sum != 0 {
		t.Fatalf("revenue = %d, want the refunded order out", sum)
	}
	after, _ := st.GetPaymentOrder(o.ID)
	if after.RefundSource != model.RefundByProvider || after.RefundedAt == 0 {
		t.Fatalf("order = %+v", after)
	}
	if _, err := m.RefundOrder(ctx, o.ID, false); !isCode(err, "err.notRefundable") {
		t.Fatalf("refund to balance after the provider's: %v", err)
	}
	m.providerRefunded(ctx, o) // the same notice again
	if b := balanceOf(t, st, u.ID); b != 0 {
		t.Fatalf("balance = %d after a repeated notice", b)
	}
}

// A refunded top-up comes off the balance — only as far as the balance goes — and the
// referrer's bonus from it comes off theirs, netting what they "earned".
func TestProviderRefundTopupAndReferral(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.RefMode, set.RefPercent = model.RefPercent, 10
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	ref := walletUser(t, st, "cbref")
	friend := walletUser(t, st, "cbfriend")
	_ = st.SetSubscriberRef(4242, ref.ID, 1)
	m.AttachReferrer(ctx, friend.ID, 4242)
	o, _, err := m.RequestTopupManual(ctx, i18n.RU, friend.ID, 300)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmPayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if b := balanceOf(t, st, ref.ID); b != 3000 {
		t.Fatalf("referrer bonus = %d", b)
	}
	// The friend spends 199 of the 300 before the money is clawed back.
	if _, err := m.BuyPlanFromBalance(ctx, friend.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatal(err)
	}
	o, _ = st.GetPaymentOrder(o.ID)
	m.providerRefunded(ctx, o)
	if b := balanceOf(t, st, friend.ID); b != 0 {
		t.Fatalf("friend balance = %d, want what was left taken", b)
	}
	if b := balanceOf(t, st, ref.ID); b != 0 {
		t.Fatalf("referrer balance = %d, want the bonus taken back", b)
	}
	if w, _ := st.GetWallet(ref.ID); w.EarnedKop != 0 {
		t.Fatalf("referrer earned = %d, want it netted out", w.EarnedKop)
	}
}

// An order the operator already refunded to the balance, then charged back: the
// money part comes off the balance again, so the user is not paid twice.
func TestProviderRefundAfterBalanceRefund(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "twice")
	o := paidOrder(t, m, u.ID, plan.ID)
	if _, err := m.RefundOrder(ctx, o.ID, false); err != nil {
		t.Fatal(err)
	}
	o, _ = st.GetPaymentOrder(o.ID)
	m.providerRefunded(ctx, o)
	if b := balanceOf(t, st, u.ID); b != 0 {
		t.Fatalf("balance = %d, want the balance refund taken back", b)
	}
}

// A user whose paid term lapsed gets one code, attached to their next payment, after
// the delay — not before, not twice, and not once they are back.
func TestWinbackCode(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.Winback = model.WinbackSettings{Enabled: true, AfterDays: 3, Percent: 25, ValidDays: 5}
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	gone := walletUser(t, st, "gone")
	back := walletUser(t, st, "back")
	paidOrder(t, m, gone.ID, plan.ID)
	paidOrder(t, m, back.ID, plan.ID)
	// Both terms end; the sweep moves them to the free plan and records the lapse.
	now := time.Now().Unix()
	for _, id := range []int64{gone.ID, back.ID} {
		if err := m.CancelUserPlan(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	paidOrder(t, m, back.ID, plan.ID) // came back on their own
	set, _ = st.GetSettings()

	m.runWinback(set, now+86400) // a day on: too early
	if s, _ := st.WinbackStats(); s.Sent != 0 {
		t.Fatalf("sent %d before the delay", s.Sent)
	}
	m.winbackAt.Store(0)
	m.runWinback(set, now+4*86400)
	m.winbackAt.Store(0)
	m.runWinback(set, now+4*86400+60) // again: nothing new
	if s, _ := st.WinbackStats(); s.Sent != 1 {
		t.Fatalf("sent %d codes, want exactly one", s.Sent)
	}
	w, _ := st.GetWallet(gone.ID)
	p, err := st.GetPromo(w.PromoID)
	if err != nil || p.Value != 25 || p.MaxUses != 1 {
		t.Fatalf("attached code = %+v, %v", p, err)
	}
	if q := m.QuotePlan(*gone, plan); q.DiscountRub == 0 {
		t.Fatalf("the code does not apply: %+v", q)
	}
	if wb, _ := st.GetWallet(back.ID); wb.PromoID != 0 {
		t.Fatal("a returning user got a code")
	}
	list, _ := st.ListPromos()
	for _, c := range list {
		if c.ID == p.ID {
			t.Fatal("a personal code shows in the operator's list")
		}
	}
}

// The funnel counts the users who joined in the period and how far they got.
func TestFunnel(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	a := walletUser(t, st, "fa")
	b := walletUser(t, st, "fb")
	walletUser(t, st, "fc")
	old := walletUser(t, st, "fold")
	_ = st.SetUserCreatedAt(old.ID, time.Now().AddDate(0, 0, -60).Unix())
	paidOrder(t, m, a.ID, plan.ID)
	paidOrder(t, m, a.ID, plan.ID)
	paidOrder(t, m, b.ID, plan.ID)
	f, err := m.Funnel(30)
	if err != nil || f.Joined != 3 || f.Paid != 2 || f.Renewed != 1 {
		t.Fatalf("funnel = %+v, %v", f, err)
	}
	if all, _ := m.Funnel(0); all.Joined != 4 {
		t.Fatalf("all time joined = %d", all.Joined)
	}
}

// A chargeback of the newest order takes back only its own month: the older one
// was paid for too.
func TestChargebackNewestKeepsOlderTime(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "twomonths")
	paidOrder(t, m, u.ID, plan.ID)
	newest := paidOrder(t, m, u.ID, plan.ID)
	before, _ := st.GetUser(u.ID)
	m.providerRefunded(ctx, newest)
	after, _ := st.GetUser(u.ID)
	if after.PlanID != plan.ID || after.ExpireAt != before.ExpireAt-30*86400 {
		t.Fatalf("plan %d, cut %d s; want the plan kept and 30 days off", after.PlanID, before.ExpireAt-after.ExpireAt)
	}
}

// Taking a plan back over a refund is not the user leaving: no win-back code follows.
func TestNoWinbackAfterRefund(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.Winback = model.WinbackSettings{Enabled: true, AfterDays: 1, Percent: 20, ValidDays: 5}
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	refunded := walletUser(t, st, "refunded")
	o := paidOrder(t, m, refunded.ID, plan.ID)
	if _, err := m.RefundOrder(ctx, o.ID, true); err != nil {
		t.Fatal(err)
	}
	charged := walletUser(t, st, "charged")
	paidOrder(t, m, charged.ID, plan.ID)
	c := paidOrder(t, m, charged.ID, plan.ID)
	m.providerRefunded(ctx, c)
	if err := m.CancelUserPlan(ctx, charged.ID); err != nil { // left afterwards, by choice
		t.Fatal(err)
	}
	set, _ = st.GetSettings()
	m.runWinback(set, time.Now().Unix()+2*86400)
	if s, _ := st.WinbackStats(); s.Sent != 0 {
		t.Fatalf("sent %d codes to users whose money went back", s.Sent)
	}
}

// Bonus days a payment gave the referrer go back with a chargeback of it; money that
// could not be taken back is reported, not lost silently.
func TestChargebackReferralDaysAndShortfall(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.RefMode, set.RefDays = model.RefDays, 7
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	ref := walletUser(t, st, "dref")
	friend := walletUser(t, st, "dfriend")
	_ = st.SetSubscriberRef(7777, ref.ID, 1)
	m.AttachReferrer(ctx, friend.ID, 7777)
	o := paidOrder(t, m, friend.ID, plan.ID)
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 7 {
		t.Fatalf("banked = %d", w.RefBonusDays)
	}
	r, err := st.ProviderRefundOrder(o.ID, time.Now().Unix())
	if err != nil || r.RefDays != 7 {
		t.Fatalf("refund = %+v, %v", r, err)
	}
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 0 {
		t.Fatalf("banked after the chargeback = %d", w.RefBonusDays)
	}

	spender := walletUser(t, st, "spender")
	top, _, err := m.RequestTopupManual(ctx, i18n.RU, spender.ID, 300)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmPayment(ctx, top.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, spender.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatal(err)
	}
	r, err = st.ProviderRefundOrder(top.ID, time.Now().Unix())
	if err != nil || r.TakenKop != 10100 || r.ShortKop != 19900 {
		t.Fatalf("top-up chargeback = %+v, %v; want 101 ₽ taken, 199 ₽ short", r, err)
	}
}

// A code with nowhere to go — no bot to tell, another live code attached — is not
// minted; spent and expired codes do not keep the promo field up.
func TestWinbackNowhereAndLivePromos(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.Winback = model.WinbackSettings{Enabled: true, AfterDays: 1, Percent: 20, ValidDays: 5}
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	if err := m.SavePromo(&model.PromoCode{Code: "LIVE10", Kind: model.PromoPercent, Value: 10, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	u := walletUser(t, st, "notg")
	paidOrder(t, m, u.ID, plan.ID)
	if _, err := m.RedeemPromo(ctx, u.ID, "LIVE10"); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelUserPlan(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	set, _ = st.GetSettings()
	m.runWinback(set, time.Now().Unix()+2*86400)
	if s, _ := st.WinbackStats(); s.Sent != 0 {
		t.Fatalf("minted %d codes nobody can use", s.Sent)
	}
	if err := m.SavePromo(&model.PromoCode{Code: "GONE", Kind: model.PromoPercent, Value: 10, Enabled: true,
		ExpiresAt: time.Now().Unix() - 60}); err != nil {
		t.Fatal(err)
	}
	if n := st.CountEnabledPromos(); n != 1 {
		t.Fatalf("live codes = %d, want the expired one left out", n)
	}
}
