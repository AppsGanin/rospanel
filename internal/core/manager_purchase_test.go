package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

type purchaseEnv struct {
	m      *Manager
	st     *store.Store
	a, b   *model.TariffPlan // a: 300 ₽ / 30 d, 2 devices, +100 ₽ each up to 3; b: 600 ₽ / 30 d
	userID int64
}

func newPurchaseEnv(t *testing.T) purchaseEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "buy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.ExecForTest(`UPDATE settings SET billing_enabled = 1, wallet_enabled = 1, plan_change = 1,
		traffic_packs = '[{"gb":10,"price_rub":150}]'`); err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: st}
	a := &model.TariffPlan{Slug: "a", Name: "A", PriceRub: 300, PeriodDays: 30, DeviceLimit: 2,
		DataLimit: 50 << 30, DevicePrice: 100, DeviceMax: 3, Enabled: true}
	b := &model.TariffPlan{Slug: "b", Name: "B", PriceRub: 600, PeriodDays: 30, DeviceLimit: 5, Enabled: true}
	for _, p := range []*model.TariffPlan{a, b} {
		if err := m.SaveTariffPlan(p); err != nil {
			t.Fatal(err)
		}
	}
	u, err := st.CreateUser("buyer", "uuid-buyer", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return purchaseEnv{m: m, st: st, a: a, b: b, userID: u.ID}
}

func (e purchaseEnv) fund(t *testing.T, rub int64) {
	t.Helper()
	if _, err := e.st.AdjustBalance(e.userID, rub*100, "test", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
}

func (e purchaseEnv) user(t *testing.T) model.User {
	t.Helper()
	u, err := e.st.GetUser(e.userID)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

func (e purchaseEnv) balance(t *testing.T) int64 {
	w, _ := e.st.GetWalletLite(e.userID)
	return w.BalanceKop
}

// Extra devices bought with the plan are paid for, added to the cap, and kept — and
// paid for — at renewal.
func TestPurchaseWithDevices(t *testing.T) {
	t.Parallel()
	e := newPurchaseEnv(t)
	e.fund(t, 1000)
	ctx := context.Background()
	p := Purchase{Kind: model.OrderPlan, PlanID: e.a.ID, Periods: 1, Devices: 2}
	q, err := e.m.QuotePurchase(e.user(t), p)
	if err != nil || q.PriceRub != 500 || q.DevicesRub != 200 {
		t.Fatalf("quote = %+v %v", q, err)
	}
	if _, err := e.m.BuyFromBalance(ctx, e.userID, p, e.user(t).ExpireAt); err != nil {
		t.Fatal(err)
	}
	u := e.user(t)
	if u.DeviceLimit != 4 || u.ExtraDevices != 2 || e.balance(t) != 500_00 {
		t.Fatalf("after purchase: limit %d extra %d balance %d", u.DeviceLimit, u.ExtraDevices, e.balance(t))
	}
	// Renewal keeps the devices and charges for them.
	if q := e.m.QuotePlanFor(u, e.a, 1); q.PriceRub != 500 || q.Devices != 2 {
		t.Fatalf("renewal quote = %+v", q)
	}
	if _, err := e.m.BuyPlanFromBalance(ctx, e.userID, e.a.ID, u.ExpireAt, 1); err != nil {
		t.Fatal(err)
	}
	u2 := e.user(t)
	if u2.DeviceLimit != 4 || u2.ExtraDevices != 2 || e.balance(t) != 0 || u2.ExpireAt-u.ExpireAt != 30*86400 {
		t.Fatalf("after renewal: %+v balance %d", u2, e.balance(t))
	}
	// Renewing the running term keeps the devices held, whatever is asked.
	if q, err := e.m.QuotePurchase(u2, Purchase{Kind: model.OrderPlan, PlanID: e.a.ID, Devices: 3}); err != nil || q.Devices != 2 {
		t.Fatalf("renewal with more devices = %+v %v", q, err)
	}
	stranger, _ := e.st.CreateUser("stranger", "uuid-s", "pw", "tok-s", 0, 0, 0)
	if _, err := e.m.QuotePurchase(*stranger, Purchase{Kind: model.OrderPlan, PlanID: e.a.ID, Devices: 4}); err == nil {
		t.Fatal("more devices than the plan sells were quoted")
	}
}

// Devices added mid-term cost their share of the term left; traffic adds to the quota
// until the next reset.
func TestAddonsMidTerm(t *testing.T) {
	t.Parallel()
	e := newPurchaseEnv(t)
	ctx := context.Background()
	if err := e.m.ApplyPlanToUser(ctx, e.userID, e.a.ID, false); err != nil {
		t.Fatal(err)
	}
	// 15 of 30 days left: a device at 100 ₽/period costs 50 ₽.
	half := time.Now().Unix() + 15*86400
	if err := e.st.ExecForTest(`UPDATE users SET expire_at = ? WHERE id = ?`, half, e.userID); err != nil {
		t.Fatal(err)
	}
	e.fund(t, 300)
	dev := Purchase{Kind: model.OrderDevices, Devices: 1}
	q, err := e.m.QuotePurchase(e.user(t), dev)
	if err != nil || q.PriceRub != 50 {
		t.Fatalf("device quote = %+v %v", q, err)
	}
	if _, err := e.m.BuyFromBalance(ctx, e.userID, dev, half+1); err == nil {
		t.Fatal("a purchase for a term the screen did not show went through")
	}
	if _, err := e.m.BuyFromBalance(ctx, e.userID, dev, half); err != nil {
		t.Fatal(err)
	}
	u := e.user(t)
	if u.DeviceLimit != 3 || u.ExtraDevices != 1 || u.ExpireAt != half {
		t.Fatalf("after devices: %+v", u)
	}
	tr := Purchase{Kind: model.OrderTraffic, Pack: 0}
	if _, err := e.m.BuyFromBalance(ctx, e.userID, tr, AnyExpiry); err != nil {
		t.Fatal(err)
	}
	u = e.user(t)
	if u.DataLimit != 60<<30 || u.PackData != 10<<30 || e.balance(t) != 100_00 {
		t.Fatalf("after pack: limit %d pack %d balance %d", u.DataLimit, u.PackData, e.balance(t))
	}
	// The quota starts over: the pack goes.
	if err := e.st.ResetUserQuota(e.userID, time.Now().Unix(), 0, 0); err != nil {
		t.Fatal(err)
	}
	if u = e.user(t); u.DataLimit != 50<<30 || u.PackData != 0 {
		t.Fatalf("after reset: limit %d pack %d", u.DataLimit, u.PackData)
	}
}

// A dearer plan keeps the end date and costs the difference for the days left; a
// cheaper one is free and turns the days left into more days.
func TestPlanChange(t *testing.T) {
	t.Parallel()
	e := newPurchaseEnv(t)
	ctx := context.Background()
	e.fund(t, 600)
	if _, err := e.m.BuyPlanFromBalance(ctx, e.userID, e.b.ID, e.user(t).ExpireAt, 1); err != nil {
		t.Fatal(err)
	}
	end := time.Now().Unix() + 10*86400
	if err := e.st.ExecForTest(`UPDATE users SET expire_at = ? WHERE id = ?`, end, e.userID); err != nil {
		t.Fatal(err)
	}
	// B (600) → A (300): 10 days of B are worth 20 days of A.
	down := Purchase{Kind: model.OrderChange, PlanID: e.a.ID, Devices: 0}
	q, err := e.m.QuotePurchase(e.user(t), down)
	if err != nil || q.Upgrade || q.TotalRub != 0 {
		t.Fatalf("downgrade quote = %+v %v", q, err)
	}
	if got := q.ExpireAt - time.Now().Unix(); got < 20*86400-60 || got > 20*86400+60 {
		t.Fatalf("downgrade gives %d s, want ~20 days", got)
	}
	if _, err := e.m.BuyFromBalance(ctx, e.userID, down, end); err != nil {
		t.Fatal(err)
	}
	u := e.user(t)
	if u.PlanID != e.a.ID || u.ExpireAt != q.ExpireAt || u.DeviceLimit != 2 {
		t.Fatalf("after downgrade: %+v", u)
	}
	// A (300) → B (600) with ~20 days left: pay ~200 ₽, end date stays.
	up := Purchase{Kind: model.OrderChange, PlanID: e.b.ID, Devices: KeepDevices}
	q2, err := e.m.QuotePurchase(u, up)
	if err != nil || !q2.Upgrade || q2.PriceRub < 199 || q2.PriceRub > 201 {
		t.Fatalf("upgrade quote = %+v %v", q2, err)
	}
	if _, err := e.m.BuyFromBalance(ctx, e.userID, up, u.ExpireAt); err == nil {
		t.Fatal("an upgrade with no balance went through")
	}
	e.fund(t, int64(q2.PriceRub))
	if _, err := e.m.BuyFromBalance(ctx, e.userID, up, u.ExpireAt); err != nil {
		t.Fatal(err)
	}
	u2 := e.user(t)
	if u2.PlanID != e.b.ID || u2.ExpireAt != u.ExpireAt || e.balance(t) != 0 {
		t.Fatalf("after upgrade: %+v balance %d", u2, e.balance(t))
	}
}

// A change paid at a provider applies when the money arrives — unless the term it
// was priced for is gone; then the money stays on the balance.
func TestChangeOrderStale(t *testing.T) {
	t.Parallel()
	e := newPurchaseEnv(t)
	ctx := context.Background()
	if err := e.m.ApplyPlanToUser(ctx, e.userID, e.a.ID, false); err != nil {
		t.Fatal(err)
	}
	u := e.user(t)
	up := Purchase{Kind: model.OrderChange, PlanID: e.b.ID, Devices: KeepDevices}
	d, _, err := e.m.purchaseDraft("ru", e.userID, up)
	if err != nil {
		t.Fatal(err)
	}
	order, err := e.st.CreateOrder(d, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	// The user renews A meanwhile: the term the change was priced for is gone.
	if err := e.st.ExecForTest(`UPDATE users SET expire_at = expire_at + 86400 WHERE id = ?`, e.userID); err != nil {
		t.Fatal(err)
	}
	res, err := e.m.confirmOrderPaid(order, time.Now().Unix())
	if err != nil || !res.Claimed || res.PlanApplied {
		t.Fatalf("confirm = %+v %v", res, err)
	}
	after := e.user(t)
	if after.PlanID != e.a.ID || e.balance(t) != int64(order.AmountRub)*100 {
		t.Fatalf("stale change: plan %d balance %d (want %d on A)", after.PlanID, e.balance(t), order.AmountRub*100)
	}
	_ = u
	// A fresh one applies.
	d2, _, err := e.m.purchaseDraft("ru", e.userID, up)
	if err != nil {
		// the balance may now cover it — then it is a balance purchase
		if _, err := e.m.BuyFromBalance(ctx, e.userID, up, AnyExpiry); err != nil {
			t.Fatal(err)
		}
	} else {
		o2, _ := e.st.CreateOrder(d2, time.Now().Unix())
		if res, err := e.m.confirmOrderPaid(o2, time.Now().Unix()); err != nil || !res.PlanApplied {
			t.Fatalf("fresh confirm = %+v %v", res, err)
		}
	}
	if got := e.user(t); got.PlanID != e.b.ID || got.ExpireAt != after.ExpireAt {
		t.Fatalf("fresh change: %+v", got)
	}
}

// Switching back and forth keeps the traffic counter; a plan held for nothing is
// worth nothing in a change.
func TestPlanChangeKeepsUsage(t *testing.T) {
	t.Parallel()
	e := newPurchaseEnv(t)
	ctx := context.Background()
	e.fund(t, 300)
	if _, err := e.m.BuyPlanFromBalance(ctx, e.userID, e.a.ID, e.user(t).ExpireAt, 1); err != nil {
		t.Fatal(err)
	}
	c := &model.TariffPlan{Slug: "c", Name: "C", PriceRub: 300, PeriodDays: 30, DeviceLimit: 2, DataLimit: 50 << 30, Enabled: true}
	if err := e.m.SaveTariffPlan(c); err != nil {
		t.Fatal(err)
	}
	if err := e.st.ExecForTest(`UPDATE users SET used_up = 40 << 30 WHERE id = ?`, e.userID); err != nil {
		t.Fatal(err)
	}
	u := e.user(t)
	if _, err := e.m.BuyFromBalance(ctx, e.userID, Purchase{Kind: model.OrderChange, PlanID: c.ID, Devices: KeepDevices}, u.ExpireAt); err != nil {
		t.Fatal(err)
	}
	if got := e.user(t); got.PlanID != c.ID || got.UsedUp != 40<<30 {
		t.Fatalf("after change: plan %d used %d", got.PlanID, got.UsedUp)
	}
	// Assigned by hand, never bought: its days are worth nothing.
	other, _ := e.st.CreateUser("given", "uuid-g", "pw", "tok-g", 0, 0, 0)
	if err := e.m.ApplyPlanToUser(ctx, other.ID, e.b.ID, false); err != nil {
		t.Fatal(err)
	}
	g, _ := e.st.GetUser(other.ID)
	q, err := e.m.QuotePurchase(*g, Purchase{Kind: model.OrderChange, PlanID: e.a.ID, Devices: 0})
	if err != nil || !q.Upgrade || q.PriceRub < 290 {
		t.Fatalf("a given plan's change = %+v %v", q, err)
	}
}

// Two taps of the same add-on button buy it once.
func TestAddonDoubleTap(t *testing.T) {
	t.Parallel()
	e := newPurchaseEnv(t)
	ctx := context.Background()
	e.fund(t, 1000)
	if _, err := e.m.BuyPlanFromBalance(ctx, e.userID, e.a.ID, e.user(t).ExpireAt, 1); err != nil {
		t.Fatal(err)
	}
	stamp := PurchaseStamp(e.user(t))
	p := Purchase{Kind: model.OrderTraffic, Pack: 0}
	if _, err := e.m.BuyFromBalance(ctx, e.userID, p, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.BuyFromBalance(ctx, e.userID, p, stamp); err == nil {
		t.Fatal("the second tap bought a second pack")
	}
	if got := e.user(t); got.PackData != 10<<30 {
		t.Fatalf("pack = %d", got.PackData)
	}
}
