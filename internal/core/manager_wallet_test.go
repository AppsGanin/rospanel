package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// walletFixture is a manager with billing, manual payment and the wallet on, a free
// plan and a paid one (199 ₽ / 30 days).
func walletFixture(t *testing.T) (*Manager, *store.Store, *model.TariffPlan) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wallet.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st}
	free := &model.TariffPlan{Slug: "w-free", Name: "Free", Enabled: true}
	paid := &model.TariffPlan{Slug: "w-std", Name: "Std", PriceRub: 199, PeriodDays: 30, Enabled: true}
	for _, p := range []*model.TariffPlan{free, paid} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save plan: %v", err)
		}
	}
	set, _ := st.GetSettings()
	set.BillingEnabled = true
	set.BillingFreePlanID = free.ID
	set.BillingManualEnabled = true
	set.WalletEnabled = true
	set.WalletTopupMin = 10
	set.RefMode = model.RefOff
	set.RefPercent, set.RefDays = 10, 7
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatalf("billing settings: %v", err)
	}
	return m, st, paid
}

func walletUser(t *testing.T, st *store.Store, name string) *model.User {
	t.Helper()
	u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return u
}

func balanceOf(t *testing.T, st *store.Store, id int64) int64 {
	t.Helper()
	w, err := st.GetWallet(id)
	if err != nil {
		t.Fatalf("wallet: %v", err)
	}
	return w.BalanceKop
}

func credit(t *testing.T, m *Manager, id, kop int64) {
	t.Helper()
	if _, err := m.AdjustBalance(context.Background(), id, kop, "test"); err != nil {
		t.Fatalf("credit: %v", err)
	}
}

// The balance pays first; only the rest is invoiced, rounded up to a whole rouble so
// a kopeck remainder stays on the balance.
func TestQuotePlanBalanceAndDiscount(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "quote")

	q := m.QuotePlan(*u, plan)
	if q.MoneyRub != 199 || q.BalanceKop != 0 {
		t.Fatalf("empty balance: %+v", q)
	}

	credit(t, m, u.ID, 5050) // 50.50 ₽
	q = m.QuotePlan(*u, plan)
	if q.MoneyRub != 149 || q.BalanceKop != 5000 {
		t.Fatalf("50.50 on the balance: want 149 ₽ + 50.00 from balance, got %+v", q)
	}

	promo := &model.PromoCode{Code: "HALF", Kind: model.PromoPercent, Value: 50, Enabled: true}
	if err := m.SavePromo(promo); err != nil {
		t.Fatalf("save promo: %v", err)
	}
	if _, err := m.RedeemPromo(context.Background(), u.ID, "half"); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	q = m.QuotePlan(*u, plan)
	// 199 − 99 = 100 ₽; 50.50 covers 50.00 of it (rounding the money part up).
	if q.DiscountRub != 99 || q.TotalRub != 100 || q.MoneyRub != 50 || q.BalanceKop != 5000 {
		t.Fatalf("discounted: %+v", q)
	}
}

// A manual order the balance helps pay: once confirmed the plan is active, the
// balance spent and the promo used up.
func TestPartPaymentConfirmGrantsPlanAndSpendsBalance(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "part")
	credit(t, m, u.ID, 5000)

	order, _, err := m.RequestPlanPayment(context.Background(), i18n.RU, u.ID, plan.ID, 1)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.AmountRub != 149 || order.BalanceKop != 5000 {
		t.Fatalf("order asks for %d ₽ + %d kop from balance, want 149 + 5000", order.AmountRub, order.BalanceKop)
	}
	if err := m.ConfirmPayment(context.Background(), order.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	after, _ := st.GetUser(u.ID)
	if after.PlanID != plan.ID || after.ExpireAt <= time.Now().Unix() {
		t.Fatalf("plan not granted: plan=%d expire=%d", after.PlanID, after.ExpireAt)
	}
	if b := balanceOf(t, st, u.ID); b != 0 {
		t.Fatalf("balance after purchase = %d, want 0", b)
	}
	// Revenue counts the money that came in, not the balance spent.
	if sum, _ := st.PaidSumSince(0); sum != 149 {
		t.Fatalf("revenue = %d, want 149", sum)
	}
}

// If the balance was spent between the invoice and the payment, the money lands on
// the balance and no plan is granted — nothing is taken twice or lost.
func TestPartPaymentWithSpentBalanceKeepsTheMoney(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "spent")
	credit(t, m, u.ID, 5000)
	order, _, err := m.RequestPlanPayment(context.Background(), i18n.RU, u.ID, plan.ID, 1)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	credit(t, m, u.ID, -3000) // spent elsewhere meanwhile
	if err := m.ConfirmPayment(context.Background(), order.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	after, _ := st.GetUser(u.ID)
	if after.PlanID == plan.ID {
		t.Fatal("plan granted although the balance no longer covered its part")
	}
	if b := balanceOf(t, st, u.ID); b != 2000+14900 {
		t.Fatalf("balance = %d, want the 20 ₽ left plus the 149 ₽ paid", b)
	}
	// No plan came of it, so it is a top-up, not a first purchase.
	if got, _ := st.GetPaymentOrder(order.ID); got.Kind != model.OrderTopup {
		t.Fatalf("undelivered order kind = %q", got.Kind)
	}
	if bought, _ := st.HasBoughtPlan(u.ID); bought {
		t.Fatal("an undelivered order counts as a plan bought")
	}
}

func TestBuyFromBalanceAndTopup(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "buyer")

	if _, err := m.BuyPlanFromBalance(context.Background(), u.ID, plan.ID, AnyExpiry, 1); err == nil {
		t.Fatal("an empty balance bought a plan")
	}
	order, _, err := m.RequestTopupManual(context.Background(), i18n.RU, u.ID, 250)
	if err != nil {
		t.Fatalf("topup order: %v", err)
	}
	if order.Kind != model.OrderTopup || order.PlanID != 0 {
		t.Fatalf("topup order = %+v", order)
	}
	if err := m.ConfirmPayment(context.Background(), order.ID); err != nil {
		t.Fatalf("confirm topup: %v", err)
	}
	if b := balanceOf(t, st, u.ID); b != 25000 {
		t.Fatalf("balance after topup = %d, want 25000", b)
	}
	bought, err := m.BuyPlanFromBalance(context.Background(), u.ID, plan.ID, AnyExpiry, 1)
	if err != nil {
		t.Fatalf("buy from balance: %v", err)
	}
	if bought.AmountRub != 0 || bought.Provider != model.BalanceProvider || bought.Status != "paid" {
		t.Fatalf("balance order = %+v", bought)
	}
	if b := balanceOf(t, st, u.ID); b != 5100 {
		t.Fatalf("balance after purchase = %d, want 5100", b)
	}
	// The top-up is revenue once; the purchase from it is not revenue again.
	if sum, _ := st.PaidSumSince(0); sum != 250 {
		t.Fatalf("revenue = %d, want 250", sum)
	}
	stats, _ := st.PaidByProvider()
	for _, p := range stats {
		if p.Provider == model.BalanceProvider {
			t.Fatalf("balance purchases show up as a revenue provider: %+v", p)
		}
	}
}

// A plan about to run out renews itself from the balance, extending from the old
// expiry; without the money, or with renewal off, it is left to expire.
func TestAutoRenewFromBalance(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	rich := walletUser(t, st, "rich")
	poor := walletUser(t, st, "poor")
	off := walletUser(t, st, "off")
	for _, u := range []*model.User{rich, poor, off} {
		if err := m.ApplyPlanToUser(context.Background(), u.ID, plan.ID, false); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	credit(t, m, rich.ID, 30000)
	credit(t, m, poor.ID, 10000)
	credit(t, m, off.ID, 30000)
	if err := st.SetAutoRenew(off.ID, false); err != nil {
		t.Fatal(err)
	}
	// Everyone's period ends in 30 minutes.
	soon := time.Now().Unix() + 1800
	for _, u := range []*model.User{rich, poor, off} {
		if err := st.SetUserLimits(u.ID, 0, soon, 0); err != nil {
			t.Fatalf("limits: %v", err)
		}
	}
	if err := m.EnforceBilling(time.Now().Unix()); err != nil {
		t.Fatalf("enforce: %v", err)
	}
	r, _ := st.GetUser(rich.ID)
	if want := soon + 30*86400; r.ExpireAt != want {
		t.Fatalf("rich expire = %d, want %d (extended from the old end)", r.ExpireAt, want)
	}
	if b := balanceOf(t, st, rich.ID); b != 30000-19900 {
		t.Fatalf("rich balance = %d", b)
	}
	for _, u := range []*model.User{poor, off} {
		got, _ := st.GetUser(u.ID)
		if got.ExpireAt != soon {
			t.Fatalf("%s was renewed (expire %d)", u.Name, got.ExpireAt)
		}
	}
}

// A referrer earns a share of every payment, or of the first only.
func TestReferralPercentReward(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	set, _ := st.GetSettings()
	set.RefMode, set.RefPercent, set.RefFirstOnly = model.RefPercent, 10, true
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatalf("settings: %v", err)
	}
	ref := walletUser(t, st, "ref")
	friend := walletUser(t, st, "friend")
	if err := st.SetSubscriberRef(555, ref.ID, 1); err != nil {
		t.Fatal(err)
	}
	m.AttachReferrer(context.Background(), friend.ID, 555)
	if w, _ := st.GetWallet(friend.ID); w.ReferrerID != ref.ID {
		t.Fatalf("referrer = %d, want %d", w.ReferrerID, ref.ID)
	}

	pay := func() {
		order, _, err := m.RequestPlanPayment(context.Background(), i18n.RU, friend.ID, plan.ID, 1)
		if err != nil {
			t.Fatalf("order: %v", err)
		}
		if err := m.ConfirmPayment(context.Background(), order.ID); err != nil {
			t.Fatalf("confirm: %v", err)
		}
	}
	pay()
	if b := balanceOf(t, st, ref.ID); b != 1990 {
		t.Fatalf("referrer balance = %d, want 19.90 ₽", b)
	}
	pay() // a renewal: first-only pays nothing more
	if b := balanceOf(t, st, ref.ID); b != 1990 {
		t.Fatalf("referrer paid for a second payment: %d", b)
	}
	w, _ := st.GetWallet(ref.ID)
	if w.Invited != 1 || w.Paying != 1 || w.EarnedKop != 1990 {
		t.Fatalf("referrer standing = %+v", w)
	}
}

// Days mode: onto the referrer's running plan, or banked until they buy one.
func TestReferralDaysBankedThenSpent(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	set, _ := st.GetSettings()
	set.RefMode, set.RefDays = model.RefDays, 7
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatalf("settings: %v", err)
	}
	ref := walletUser(t, st, "dref")
	friend := walletUser(t, st, "dfriend")
	_ = st.SetSubscriberRef(777, ref.ID, 1)
	m.AttachReferrer(context.Background(), friend.ID, 777)

	order, _, err := m.RequestPlanPayment(context.Background(), i18n.RU, friend.ID, plan.ID, 1)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if err := m.ConfirmPayment(context.Background(), order.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 7 {
		t.Fatalf("banked days = %d, want 7", w.RefBonusDays)
	}
	// The referrer's own purchase carries the banked week.
	own, _, err := m.RequestPlanPayment(context.Background(), i18n.RU, ref.ID, plan.ID, 1)
	if err != nil {
		t.Fatalf("own order: %v", err)
	}
	before := time.Now().Unix()
	if err := m.ConfirmPayment(context.Background(), own.ID); err != nil {
		t.Fatalf("own confirm: %v", err)
	}
	r, _ := st.GetUser(ref.ID)
	if r.ExpireAt < before+37*86400 {
		t.Fatalf("expire = %d, want 30 + 7 days", r.ExpireAt-before)
	}
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 0 {
		t.Fatalf("banked days left = %d", w.RefBonusDays)
	}
}

func TestPromoCodes(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "promo")
	other := walletUser(t, st, "promo2")

	gift := &model.PromoCode{Code: "GIFT", Kind: model.PromoBalance, Value: 100, MaxUses: 1, Enabled: true}
	week := &model.PromoCode{Code: "WEEK", Kind: model.PromoDays, Value: 7, PlanID: plan.ID, Enabled: true}
	for _, p := range []*model.PromoCode{gift, week} {
		if err := m.SavePromo(p); err != nil {
			t.Fatalf("save %s: %v", p.Code, err)
		}
	}
	if err := m.SavePromo(&model.PromoCode{Code: "gift", Kind: model.PromoBalance, Value: 1}); err == nil {
		t.Fatal("a code differing only in case was accepted")
	}

	if _, err := m.RedeemPromo(ctx, u.ID, "gift"); err != nil {
		t.Fatalf("redeem gift: %v", err)
	}
	if b := balanceOf(t, st, u.ID); b != 10000 {
		t.Fatalf("balance = %d", b)
	}
	if _, err := m.RedeemPromo(ctx, other.ID, "GIFT"); !isCode(err, "err.promoUnavailable") {
		t.Fatalf("a used-up code worked again: %v", err)
	}
	res, err := m.RedeemPromo(ctx, u.ID, "WEEK")
	if err != nil {
		t.Fatalf("redeem week: %v", err)
	}
	got, _ := st.GetUser(u.ID)
	if got.PlanID != plan.ID || res.ExpireAt < time.Now().Unix()+7*86400-5 || res.ExpireAt > time.Now().Unix()+7*86400+5 {
		t.Fatalf("week code: plan=%d expire in %ds", got.PlanID, res.ExpireAt-time.Now().Unix())
	}
	if _, err := m.RedeemPromo(ctx, u.ID, "WEEK"); !isCode(err, "err.promoUsed") {
		t.Fatalf("a code worked twice for one user: %v", err)
	}
	if _, err := m.RedeemPromo(ctx, u.ID, "nope"); !isCode(err, "err.promoNotFound") {
		t.Fatalf("unknown code: %v", err)
	}
}

// A discount code is used up by the payment it discounted, not by being entered.
func TestDiscountConsumedOnPayment(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "disc")
	p := &model.PromoCode{Code: "MINUS50", Kind: model.PromoAmount, Value: 50, Enabled: true}
	if err := m.SavePromo(p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RedeemPromo(ctx, u.ID, "MINUS50"); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	order, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 1)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.AmountRub != 149 || order.DiscountRub != 50 || order.PromoID != p.ID {
		t.Fatalf("order = %+v", order)
	}
	if got, _ := st.GetPromo(p.ID); got.Uses != 0 {
		t.Fatal("entering the code used it up before any payment")
	}
	if err := m.ConfirmPayment(ctx, order.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if got, _ := st.GetPromo(p.ID); got.Uses != 1 {
		t.Fatalf("uses = %d, want 1", got.Uses)
	}
	if w, _ := st.GetWallet(u.ID); w.PromoID != 0 {
		t.Fatal("the used code is still attached")
	}
}

func isCode(err error, code string) bool {
	var ve *ValidationError
	return errors.As(err, &ve) && ve.Code == code
}

// A discount code serves one checkout: a later purchase with it cancels the older
// discounted order, so the code cannot pay for two periods.
func TestDiscountServesOneCheckout(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "twice")
	p := &model.PromoCode{Code: "HALF1", Kind: model.PromoPercent, Value: 50, MaxUses: 1, Enabled: true}
	if err := m.SavePromo(p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RedeemPromo(ctx, u.ID, "HALF1"); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	first, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 1)
	if err != nil || first.DiscountRub == 0 {
		t.Fatalf("first order: %+v %v", first, err)
	}
	credit(t, m, u.ID, 10000)
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("buy from balance: %v", err)
	}
	afterBuy, _ := st.GetUser(u.ID)
	balAfterBuy := balanceOf(t, st, u.ID)
	// Paying the superseded order anyway: the money goes to the balance, and it buys
	// no second discounted period.
	if err := m.ConfirmPayment(ctx, first.ID); err != nil {
		t.Fatalf("confirm superseded: %v", err)
	}
	if got, _ := st.GetUser(u.ID); got.ExpireAt != afterBuy.ExpireAt {
		t.Fatal("the superseded order extended the plan")
	}
	if b := balanceOf(t, st, u.ID); b != balAfterBuy+int64(first.AmountRub)*100 {
		t.Fatalf("balance = %d, want %d", b, balAfterBuy+int64(first.AmountRub)*100)
	}
	if got, _ := st.GetPromo(p.ID); got.Uses != 1 {
		t.Fatalf("uses = %d", got.Uses)
	}
	// With the code used, the next quote is the full price.
	u2, _ := st.GetUser(u.ID)
	if q := m.QuotePlan(*u2, plan); q.DiscountRub != 0 {
		t.Fatalf("a used code still discounts: %+v", q)
	}
}

// A days code does not put an end date on a lifetime plan, and cannot name one.
func TestDaysCodeLeavesLifetimePlanAlone(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	ctx := context.Background()
	life := &model.TariffPlan{Slug: "w-life", Name: "Life", PriceRub: 999, Enabled: true}
	if err := st.SaveTariffPlan(life); err != nil {
		t.Fatal(err)
	}
	if err := m.SavePromo(&model.PromoCode{Code: "LIFE7", Kind: model.PromoDays, Value: 7, PlanID: life.ID, Enabled: true}); !isCode(err, "err.promoPaidPlan") {
		t.Fatalf("a days code for a lifetime plan was saved: %v", err)
	}
	u := walletUser(t, st, "lifer")
	if err := m.ApplyPlanToUser(ctx, u.ID, life.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SavePromo(&model.PromoCode{Code: "ANY7", Kind: model.PromoDays, Value: 7, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RedeemPromo(ctx, u.ID, "ANY7"); !isCode(err, "err.promoLifetime") {
		t.Fatalf("days onto a lifetime plan: %v", err)
	}
	if got, _ := st.GetUser(u.ID); got.ExpireAt != 0 {
		t.Fatalf("the lifetime plan got an end: %d", got.ExpireAt)
	}
}

// A plan that ended long ago is not bought again by money that lands later.
func TestAutoRenewSkipsLongExpiredPlans(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	set, _ := st.GetSettings()
	set.BillingFreePlanID = 0 // expired users then stay on their plan
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	u := walletUser(t, st, "gone")
	if err := m.ApplyPlanToUser(context.Background(), u.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Unix() - 180*86400
	if err := st.SetUserLimits(u.ID, 0, old, 0); err != nil {
		t.Fatal(err)
	}
	credit(t, m, u.ID, 19900)
	if err := m.EnforceBilling(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if b := balanceOf(t, st, u.ID); b != 19900 {
		t.Fatalf("balance = %d: a plan over for half a year was bought again", b)
	}
}

// Days are for plan purchases, not top-ups; a plan bought from the balance counts.
func TestReferralDaysOnlyForPlanPurchases(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.RefMode, set.RefDays = model.RefDays, 7
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	ref := walletUser(t, st, "tref")
	friend := walletUser(t, st, "tfriend")
	_ = st.SetSubscriberRef(888, ref.ID, 1)
	m.AttachReferrer(ctx, friend.ID, 888)
	for range 2 {
		o, _, err := m.RequestTopupManual(ctx, i18n.RU, friend.ID, 100)
		if err != nil {
			t.Fatalf("topup: %v", err)
		}
		if err := m.ConfirmPayment(ctx, o.ID); err != nil {
			t.Fatal(err)
		}
	}
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 0 {
		t.Fatalf("top-ups earned %d days", w.RefBonusDays)
	}
	// From the balance: no money came in, so no days either (the balance might be a
	// promo code's gift).
	if _, err := m.BuyPlanFromBalance(ctx, friend.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("buy: %v", err)
	}
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 0 {
		t.Fatalf("a plan bought from the balance earned %d days", w.RefBonusDays)
	}
	// Bought with money: the days are paid.
	o, _, err := m.RequestPlanPayment(ctx, i18n.RU, friend.ID, plan.ID, 1)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if err := m.ConfirmPayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 7 {
		t.Fatalf("a plan bought with money earned %d days, want 7", w.RefBonusDays)
	}
}

// One user cannot open top-up orders without end.
func TestPendingTopupsCapped(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	u := walletUser(t, st, "flood")
	for i := range maxPendingTopups {
		if _, _, err := m.RequestTopupManual(context.Background(), i18n.RU, u.ID, 100+i); err != nil {
			t.Fatalf("topup %d: %v", i, err)
		}
	}
	if _, _, err := m.RequestTopupManual(context.Background(), i18n.RU, u.ID, 999); !isCode(err, "err.topupPending") {
		t.Fatalf("an extra pending top-up: %v", err)
	}
}

// Money that arrives for an order already cancelled buys what the order was for.
func TestLatePaymentOnCancelledOrderDelivers(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "late")
	o, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CancelPayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmPayment(ctx, o.ID); err != nil {
		t.Fatalf("confirm cancelled: %v", err)
	}
	if got, _ := st.GetUser(u.ID); got.PlanID != plan.ID || got.ExpireAt <= time.Now().Unix() {
		t.Fatal("a paid cancelled order did not deliver its plan")
	}
	if err := m.ConfirmPayment(ctx, o.ID); !isCode(err, "err.orderAlreadyHandled") {
		t.Fatalf("a second confirm: %v", err)
	}
	if b := balanceOf(t, st, u.ID); b != 0 {
		t.Fatalf("balance = %d: the money went to the plan", b)
	}
}

// A payment racing a cancel is not lost: the claim takes a cancelled order too.
func TestConfirmAfterConcurrentCancel(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "racecancel")
	o, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	stale := *o // read as pending
	if err := m.CancelPayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	res, err := m.confirmOrderPaid(&stale, time.Now().Unix())
	if err != nil || !res.Claimed || !res.PlanApplied {
		t.Fatalf("claim over a concurrent cancel: %+v %v", res, err)
	}
}

// Re-saving a code whose plans were all deleted is refused, not widened to "any plan".
func TestPromoWithDeletedPlansNotWidened(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	gone := &model.TariffPlan{Slug: "w-gone", Name: "Gone", PriceRub: 50, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(gone); err != nil {
		t.Fatal(err)
	}
	p := &model.PromoCode{Code: "ONLYGONE", Kind: model.PromoPercent, Value: 90, PlanIDs: []int64{gone.ID}, Enabled: true}
	if err := m.SavePromo(p); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTariffPlan(gone.ID); err != nil {
		t.Fatal(err)
	}
	p.Note = "edited"
	if err := m.SavePromo(p); !isCode(err, "err.planNotFound") {
		t.Fatalf("re-save: %v", err)
	}
	if got, _ := st.GetPromo(p.ID); len(got.PlanIDs) != 1 {
		t.Fatalf("stored plan list changed: %v", got.PlanIDs)
	}
}

// A discount that fits no plan the user can buy is refused rather than promised.
func TestDiscountMustFitABuyablePlan(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	other := &model.TariffPlan{Slug: "w-other", Name: "Other", PriceRub: 300, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(other); err != nil {
		t.Fatal(err)
	}
	if err := m.SavePromo(&model.PromoCode{Code: "OTHERONLY", Kind: model.PromoPercent, Value: 10, PlanIDs: []int64{other.ID}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	u := walletUser(t, st, "fits")
	if err := m.ApplyPlanToUser(ctx, u.ID, plan.ID, false); err != nil { // active paid plan: only it can be bought
		t.Fatal(err)
	}
	if _, err := m.RedeemPromo(ctx, u.ID, "OTHERONLY"); !isCode(err, "err.promoNotForPlan") {
		t.Fatalf("a code for a plan the user cannot buy: %v", err)
	}
}

// Junk codes from many accounts do not lock out a real one.
func TestPromoMissBudgetSparesRealCodes(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	ctx := context.Background()
	if err := m.SavePromo(&model.PromoCode{Code: "REAL50", Kind: model.PromoBalance, Value: 50, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for range promoMissMax {
		m.promoTry.miss(now)
	}
	u := walletUser(t, st, "realcode")
	if _, err := m.RedeemPromo(ctx, u.ID, "real50"); err != nil {
		t.Fatalf("a real code refused after junk: %v", err)
	}
	if _, err := m.RedeemPromo(ctx, u.ID, "JUNK"); !isCode(err, "err.promoTooMany") {
		t.Fatalf("junk after the budget: %v", err)
	}
}

// A rouble on top of a gifted balance does not earn a whole period of days.
func TestReferralDaysNeedMostlyMoney(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.RefMode, set.RefDays = model.RefDays, 7
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	ref := walletUser(t, st, "mref")
	friend := walletUser(t, st, "mfriend")
	_ = st.SetSubscriberRef(4321, ref.ID, 1)
	m.AttachReferrer(ctx, friend.ID, 4321)
	credit(t, m, friend.ID, 19800) // a gift covering all but 1 ₽
	o, _, err := m.RequestPlanPayment(ctx, i18n.RU, friend.ID, plan.ID, 1)
	if err != nil || o.AmountRub != 1 {
		t.Fatalf("order: %+v %v", o, err)
	}
	if err := m.ConfirmPayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 0 {
		t.Fatalf("1 ₽ earned %d days", w.RefBonusDays)
	}
}

// The same offer taken twice (a double tap) buys once.
func TestStaleBalancePurchaseRefused(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "tap")
	credit(t, m, u.ID, 50000)
	seen, _ := st.GetUser(u.ID)
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, seen.ExpireAt, 1); err != nil {
		t.Fatalf("first tap: %v", err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, seen.ExpireAt, 1); !isCode(err, "err.purchaseStale") {
		t.Fatalf("second tap: %v", err)
	}
	if b := balanceOf(t, st, u.ID); b != 50000-19900 {
		t.Fatalf("balance = %d", b)
	}
}

// A switched-off user is not charged for a renewal they cannot use.
func TestAutoRenewSkipsDisabledUsers(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "off2")
	if err := m.ApplyPlanToUser(context.Background(), u.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	credit(t, m, u.ID, 30000)
	soon := time.Now().Unix() + 600
	_ = st.SetUserLimits(u.ID, 0, soon, 0)
	if _, err := st.SetUsersEnabled([]int64{u.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.EnforceBilling(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if b := balanceOf(t, st, u.ID); b != 30000 {
		t.Fatalf("a disabled user was charged: %d", b)
	}
}

// A 100% code and a balance gift buy a plan, but earn the referrer nothing.
func TestReferralNoRewardWithoutMoney(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.RefMode, set.RefDays = model.RefDays, 7
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	ref := walletUser(t, st, "fref")
	friend := walletUser(t, st, "ffriend")
	_ = st.SetSubscriberRef(999, ref.ID, 1)
	m.AttachReferrer(ctx, friend.ID, 999)
	if err := m.SavePromo(&model.PromoCode{Code: "FREE100", Kind: model.PromoPercent, Value: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RedeemPromo(ctx, friend.ID, "FREE100"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, friend.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("free purchase: %v", err)
	}
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 0 || w.BalanceKop != 0 {
		t.Fatalf("referrer earned from a free plan: %+v", w)
	}
}

// Parallel top-up requests cannot get past the cap.
func TestTopupCapHoldsUnderConcurrency(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	u := walletUser(t, st, "burst")
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = m.RequestTopupManual(context.Background(), i18n.RU, u.ID, 100+i)
		}()
	}
	wg.Wait()
	orders, _ := st.ListPaymentOrders("pending", 0, 100)
	if len(orders) > maxPendingTopups {
		t.Fatalf("%d pending top-ups, cap %d", len(orders), maxPendingTopups)
	}
}

// A limited discount is not promised to more open checkouts than it has uses.
func TestLimitedDiscountHeldByPendingOrders(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	if err := m.SavePromo(&model.PromoCode{Code: "ONLY1", Kind: model.PromoAmount, Value: 50, MaxUses: 1, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	a := walletUser(t, st, "holda")
	b := walletUser(t, st, "holdb")
	if _, err := m.RedeemPromo(ctx, a.ID, "ONLY1"); err != nil {
		t.Fatal(err)
	}
	if o, _, err := m.RequestPlanPayment(ctx, i18n.RU, a.ID, plan.ID, 1); err != nil || o.DiscountRub != 50 {
		t.Fatalf("a's order: %+v %v", o, err)
	}
	if _, err := m.RedeemPromo(ctx, b.ID, "ONLY1"); !isCode(err, "err.promoUnavailable") {
		t.Fatalf("the last use was promised twice: %v", err)
	}
}

// A lifetime plan already held cannot be bought again: that would take money and
// change nothing, and its expiry (0) cannot catch a double tap.
func TestLifetimePlanNotBoughtTwice(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	ctx := context.Background()
	life := &model.TariffPlan{Slug: "w-life2", Name: "Life", PriceRub: 100, Enabled: true}
	if err := st.SaveTariffPlan(life); err != nil {
		t.Fatal(err)
	}
	u := walletUser(t, st, "life2")
	credit(t, m, u.ID, 50000)
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, life.ID, 0, 1); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, life.ID, 0, 1); !isCode(err, "err.planLifetimeOwned") {
		t.Fatalf("second: %v", err)
	}
	if b := balanceOf(t, st, u.ID); b != 40000 {
		t.Fatalf("balance = %d, want 400 ₽", b)
	}
}

// An old order for plan A, paid after the user bought plan B, does not overwrite B:
// its money goes to the balance.
func TestOldOrderDoesNotOverwriteNewerPlan(t *testing.T) {
	t.Parallel()
	m, st, planA := walletFixture(t)
	ctx := context.Background()
	planB := &model.TariffPlan{Slug: "w-b", Name: "B", PriceRub: 500, PeriodDays: 90, Enabled: true}
	if err := st.SaveTariffPlan(planB); err != nil {
		t.Fatal(err)
	}
	u := walletUser(t, st, "switch")
	orderA, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, planA.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	credit(t, m, u.ID, 50000)
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, planB.ID, AnyExpiry, 1); err != nil {
		t.Fatalf("buy B: %v", err)
	}
	withB, _ := st.GetUser(u.ID)
	if err := m.ConfirmPayment(ctx, orderA.ID); err != nil {
		t.Fatalf("confirm A: %v", err)
	}
	got, _ := st.GetUser(u.ID)
	if got.PlanID != planB.ID || got.ExpireAt != withB.ExpireAt {
		t.Fatalf("plan B overwritten: plan=%d", got.PlanID)
	}
	if b := balanceOf(t, st, u.ID); b != 19900 {
		t.Fatalf("balance = %d, want A's 199 ₽", b)
	}
	if o, _ := st.GetPaymentOrder(orderA.ID); o.Kind != model.OrderTopup || o.Status != "paid" {
		t.Fatalf("order A = %s/%s", o.Kind, o.Status)
	}
}

// A referrer on a lifetime plan is not promised days they can never use.
func TestReferralDaysSkipLifetimeReferrer(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.RefMode, set.RefDays = model.RefDays, 7
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	life := &model.TariffPlan{Slug: "w-life3", Name: "Life", PriceRub: 100, Enabled: true}
	if err := st.SaveTariffPlan(life); err != nil {
		t.Fatal(err)
	}
	ref := walletUser(t, st, "lref")
	if err := m.ApplyPlanToUser(ctx, ref.ID, life.ID, false); err != nil {
		t.Fatal(err)
	}
	friend := walletUser(t, st, "lfriend")
	_ = st.SetSubscriberRef(1234, ref.ID, 1)
	m.AttachReferrer(ctx, friend.ID, 1234)
	o, _, err := m.RequestPlanPayment(ctx, i18n.RU, friend.ID, plan.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmPayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if w, _ := st.GetWallet(ref.ID); w.RefBonusDays != 0 {
		t.Fatalf("banked %d days for a lifetime referrer", w.RefBonusDays)
	}
}

// Days from a code taken during a trial start now, not after the trial.
func TestDaysCodeDuringTrialStartsNow(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	trial := &model.TariffPlan{Slug: "w-trial", Name: "Trial", PeriodDays: 3, Enabled: true}
	if err := st.SaveTariffPlan(trial); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	set.BillingTrialPlanID = trial.ID
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	u := walletUser(t, st, "on-trial")
	if err := m.ApplyPlanToUser(ctx, u.ID, trial.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SavePromo(&model.PromoCode{Code: "WEEK2", Kind: model.PromoDays, Value: 7, PlanID: plan.ID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	res, err := m.RedeemPromo(ctx, u.ID, "WEEK2")
	if err != nil {
		t.Fatal(err)
	}
	if left := res.ExpireAt - time.Now().Unix(); left > 7*86400+10 {
		t.Fatalf("the week started after the trial: %d s left", left)
	}
}

// Two open orders for the same lifetime plan, both paid: the second buys nothing,
// so its money goes to the balance instead of vanishing.
func TestSecondLifetimePaymentGoesToBalance(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	ctx := context.Background()
	life := &model.TariffPlan{Slug: "w-life4", Name: "Life", PriceRub: 300, Enabled: true}
	if err := st.SaveTariffPlan(life); err != nil {
		t.Fatal(err)
	}
	u := walletUser(t, st, "life4")
	first, err := st.CreateOrder(store.OrderDraft{UserID: u.ID, PlanID: life.ID, Kind: model.OrderPlan, AmountRub: 300}, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateOrder(store.OrderDraft{UserID: u.ID, PlanID: life.ID, Kind: model.OrderPlan, AmountRub: 300}, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []*model.PaymentOrder{first, second} {
		if err := m.ConfirmPayment(ctx, o.ID); err != nil {
			t.Fatalf("confirm %d: %v", o.ID, err)
		}
	}
	if got, _ := st.GetUser(u.ID); got.PlanID != life.ID {
		t.Fatal("the lifetime plan was not granted")
	}
	if b := balanceOf(t, st, u.ID); b != 30000 {
		t.Fatalf("balance = %d, want the second 300 ₽", b)
	}
}

// A user reading in English gets the English text of a validation error.
func TestUserErrorInEnglish(t *testing.T) {
	t.Parallel()
	err := invalidCode("err.promoNotFound", "такого промокода нет")
	if got := UserError(err, i18n.EN); got != "no such promo code" {
		t.Fatalf("EN = %q", got)
	}
	if got := UserError(err, i18n.RU); got != "такого промокода нет" {
		t.Fatalf("RU = %q", got)
	}
}

// Setting renewal to what it already is writes no journal line.
func TestAutoRenewUnchangedIsQuiet(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	u := walletUser(t, st, "quiet")
	for range 3 {
		if err := m.SetAutoRenew(context.Background(), u.ID, true); err != nil { // already on
			t.Fatal(err)
		}
	}
	events, _ := st.ListUserEvents(u.ID, 50, 0)
	for _, e := range events {
		if e.Action == model.EventAutoRenew {
			t.Fatal("an unchanged switch was journaled")
		}
	}
}

// A user who paid while the expired list was being walked is not downgraded.
func TestDowngradeRechecksUnderLock(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "justpaid")
	if err := m.ApplyPlanToUser(context.Background(), u.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	// The list said expired; a payment has since moved the term into the future.
	if err := m.downgradeExpired(context.Background(), u.ID, set.BillingFreePlanID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetUser(u.ID); got.PlanID != plan.ID {
		t.Fatal("a user with a running term was downgraded")
	}
}

// Several periods at once: priced with the operator's discount, the term multiplied,
// a quota the plan does not refill on its own cycle refilled every period.
func TestBuySeveralPeriods(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	ctx := context.Background()
	plan := &model.TariffPlan{Slug: "w-q", Name: "Quota", PriceRub: 100, PeriodDays: 30, DataLimit: 10 << 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	set.BillingPeriods = []model.PeriodOffer{{Periods: 3, Percent: 10}}
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatalf("settings: %v", err)
	}
	u := walletUser(t, st, "longterm")
	if q := m.QuotePlanFor(*u, plan, 3); q.TotalRub != 270 || q.PeriodDiscountRub != 30 || q.Periods != 3 {
		t.Fatalf("quote ×3 = %+v", q)
	}
	if _, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 5); !isCode(err, "err.periodsUnavailable") {
		t.Fatalf("an unsold term: %v", err)
	}
	o, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 3)
	if err != nil || o.AmountRub != 270 || o.Periods != 3 {
		t.Fatalf("order: %+v %v", o, err)
	}
	before := time.Now().Unix()
	if err := m.ConfirmPayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetUser(u.ID)
	if got.ExpireAt < before+90*86400-5 || got.ExpireAt > before+90*86400+5 {
		t.Fatalf("term = %d s, want 90 days", got.ExpireAt-before)
	}
	if got.ResetPeriod != "days:30" {
		t.Fatalf("reset = %q, want the quota refilled every period", got.ResetPeriod)
	}
}

// A refund puts the order's price back on the balance, once, and can end the plan.
func TestRefundToBalance(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "refund")
	o, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmPayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	kop, err := m.RefundOrder(ctx, o.ID, true)
	if err != nil || kop != 19900 {
		t.Fatalf("refund = %d, %v", kop, err)
	}
	if b := balanceOf(t, st, u.ID); b != 19900 {
		t.Fatalf("balance = %d", b)
	}
	if got, _ := st.GetUser(u.ID); got.PlanID == plan.ID {
		t.Fatal("the plan was not cancelled with the refund")
	}
	if _, err := m.RefundOrder(ctx, o.ID, false); !isCode(err, "err.notRefundable") {
		t.Fatalf("a second refund: %v", err)
	}
	// Revenue still counts the money: it came in, and now sits on the balance.
	if sum, _ := st.PaidSumSince(0); sum != 199 {
		t.Fatalf("revenue = %d", sum)
	}
}

// What the panel shows: who invited whom, who used a code and what it brought.
func TestReferralAndPromoViews(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.RefMode, set.RefPercent = model.RefPercent, 10
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	ref := walletUser(t, st, "vref")
	friend := walletUser(t, st, "vfriend")
	_ = st.SetSubscriberRef(5555, ref.ID, 1)
	m.AttachReferrer(ctx, friend.ID, 5555)
	p := &model.PromoCode{Code: "VIEW10", Kind: model.PromoAmount, Value: 10, Enabled: true}
	if err := m.SavePromo(p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RedeemPromo(ctx, friend.ID, "VIEW10"); err != nil {
		t.Fatal(err)
	}
	o, _, err := m.RequestPlanPayment(ctx, i18n.RU, friend.ID, plan.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmPayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	refs, _ := m.Referrals(ref.ID, 200)
	if len(refs) != 1 || refs[0].UserID != friend.ID || refs[0].PaidRub != 189 || refs[0].EarnedKop != 1890 {
		t.Fatalf("referrals = %+v", refs)
	}
	usage, _ := m.PromoUsage(p.ID)
	if len(usage.Uses) != 1 || usage.Orders != 1 || usage.RevenueRub != 189 || usage.Uses[0].DiscountRub != 10 {
		t.Fatalf("usage = %+v", usage)
	}
	stats, _ := m.ReferralStats()
	if stats.Invited != 1 || stats.Paying != 1 || stats.RevenueRub != 189 || stats.PaidOutKop != 1890 ||
		len(stats.Top) != 1 || stats.Top[0].UserID != ref.ID {
		t.Fatalf("stats = %+v", stats)
	}
}

// The "expiring soon" message says whether the balance will renew the plan.
func TestRenewalNotice(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "notice")
	if err := m.ApplyPlanToUser(context.Background(), u.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	fresh, _ := st.GetUser(u.ID)
	if got := m.renewalNotice(set, *fresh, i18n.RU); !strings.Contains(got, "не хватает 199") {
		t.Fatalf("short = %q", got)
	}
	credit(t, m, u.ID, 30000)
	if got := m.renewalNotice(set, *fresh, i18n.RU); !strings.Contains(got, "спишется 199") {
		t.Fatalf("covered = %q", got)
	}
	_ = st.SetAutoRenew(u.ID, false)
	if got := m.renewalNotice(set, *fresh, i18n.RU); got != "" {
		t.Fatalf("renewal off still promised: %q", got)
	}
}

// quotaPlan is a 30-day plan with a 10 GB quota and no cycle of its own.
func quotaPlan(t *testing.T, st *store.Store, slug string) *model.TariffPlan {
	t.Helper()
	p := &model.TariffPlan{Slug: slug, Name: "Quota " + slug, PriceRub: 100, PeriodDays: 30, DataLimit: 10 << 30, Enabled: true}
	if err := st.SaveTariffPlan(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A term bought as several periods keeps refilling every period when one more
// period is bought on top of it.
func TestPeriodRefillSurvivesNextPurchase(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	ctx := context.Background()
	plan := quotaPlan(t, st, "p-q")
	set, _ := st.GetSettings()
	set.BillingPeriods = []model.PeriodOffer{{Periods: 3, Percent: 10}}
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	u := walletUser(t, st, "refill")
	credit(t, m, u.ID, 50000)
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BuyPlanFromBalance(ctx, u.ID, plan.ID, AnyExpiry, 1); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetUser(u.ID)
	if got.ResetPeriod != "days:30" {
		t.Fatalf("reset = %q after one more period, want days:30", got.ResetPeriod)
	}
}

// A pending order for one period is not handed back for three.
func TestPendingOrderReuseMatchesPeriods(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	set, _ := st.GetSettings()
	set.BillingPeriods = []model.PeriodOffer{{Periods: 2, Percent: 50}}
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	u := walletUser(t, st, "reuse")
	one, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	two, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if two.ID == one.ID || two.Periods != 2 {
		t.Fatalf("×2 reused the ×1 order: %+v", two)
	}
}

// Refunding an older order with the plan cancelled takes off only its own days: the
// newer order's time was paid for.
func TestRefundOlderOrderKeepsNewerTime(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	u := walletUser(t, st, "older")
	var ids []int64
	for range 2 {
		o, _, err := m.RequestPlanPayment(ctx, i18n.RU, u.ID, plan.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.ConfirmPayment(ctx, o.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, o.ID)
	}
	before, _ := st.GetUser(u.ID)
	if _, err := m.RefundOrder(ctx, ids[0], true); err != nil {
		t.Fatal(err)
	}
	after, _ := st.GetUser(u.ID)
	if after.PlanID != plan.ID || after.ExpireAt != before.ExpireAt-30*86400 {
		t.Fatalf("plan %d, term cut by %d s; want the plan kept, 30 days off",
			after.PlanID, before.ExpireAt-after.ExpireAt)
	}
	if _, err := m.RefundOrder(ctx, ids[1], true); err != nil {
		t.Fatal(err)
	}
	if last, _ := st.GetUser(u.ID); last.PlanID == plan.ID {
		t.Fatal("refunding the newest order left the plan")
	}
}

// The renewal note promises only what autoRenew will do.
func TestRenewalOutlookMatchesAutoRenew(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "outlook")
	if err := m.ApplyPlanToUser(context.Background(), u.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	fresh, _ := st.GetUser(u.ID)
	if r := m.Renewal(set, *fresh); r.PriceKop == 0 || !r.On {
		t.Fatalf("running plan: %+v", r)
	}
	gone := *fresh
	gone.ExpireAt = time.Now().Unix() - 10*86400
	if r := m.Renewal(set, gone); r.PriceKop != 0 {
		t.Fatalf("a term that ended 10 days ago: %+v", r)
	}
	off := *fresh
	off.Enabled = false
	if r := m.Renewal(set, off); r.PriceKop != 0 {
		t.Fatalf("a disabled user: %+v", r)
	}
}
