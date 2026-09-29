package core

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Flexible plans: what a user can buy beyond a plan and its periods.
//
//   - Extra devices. A plan may sell devices beyond the ones it includes, at a price
//     per device per period; they are chosen with the plan, kept at renewal (and paid
//     for with it), and can be added in the middle of a term for the part of it left.
//   - Traffic packs: gigabytes on top of the quota, until it next resets or the term
//     ends.
//   - A plan change while one is active. Moving to a dearer plan keeps the end date
//     and costs the difference for the days left; moving to a cheaper one costs
//     nothing and turns what the days left are worth into days of the new plan.
//
// Each is a Purchase, quoted and paid like a plan: from the balance, at a provider
// or by hand. An order for a change or for devices is priced for the term the user
// has when it is opened; money that arrives after that term changed buys nothing and
// lands on the balance.

// Purchase is what a user buys.
type Purchase struct {
	Kind    string // model.OrderPlan, OrderChange, OrderDevices or OrderTraffic
	PlanID  int64  // plan, change: the plan bought
	Periods int    // plan: how many of its periods (0 = one)
	// Devices: for a plan or a change, the extra devices wanted (KeepDevices = the ones
	// held, on a renewal); for devices, how many to add.
	Devices int
	Pack    int // traffic: which of the settings' packs
}

// KeepDevices asks a plan purchase for the extra devices the user already holds.
const KeepDevices = -1

// PlanPurchase is a plain plan purchase keeping the devices held.
func PlanPurchase(planID int64, periods int) Purchase {
	return Purchase{Kind: model.OrderPlan, PlanID: planID, Periods: periods, Devices: KeepDevices}
}

// heldDevices is how many extra devices of plan the user keeps: the ones they hold
// when it is their plan, as far as it still sells them.
func heldDevices(u model.User, plan *model.TariffPlan) int {
	if u.PlanID != plan.ID || !plan.SellsDevices() {
		return 0
	}
	return min(u.ExtraDevices, plan.DeviceMax)
}

// wantDevices resolves a purchase's device count against what the plan sells.
func wantDevices(u model.User, plan *model.TariffPlan, n int) (int, error) {
	if n == KeepDevices {
		return heldDevices(u, plan), nil
	}
	if n <= 0 {
		return 0, nil
	}
	if !plan.SellsDevices() {
		return 0, invalidCode("err.devicesNotSold", "этот тариф не продаёт дополнительные устройства")
	}
	if n > plan.DeviceMax {
		return 0, invalidCode("err.devicesTooMany", "можно добавить не больше {{max}} устройств", map[string]any{"max": plan.DeviceMax})
	}
	return n, nil
}

// periodRub is one period of plan with extra devices.
func periodRub(plan *model.TariffPlan, devices int) int {
	return plan.PriceRub + devices*plan.DevicePrice
}

// termValueKop is what secs of a plan period priced periodRub are worth, in kopecks
// (rounded down: it is what the user is credited with).
func termValueKop(rub int, periodDays int, secs int64) int64 {
	if periodDays <= 0 || secs <= 0 {
		return 0
	}
	v := new(big.Int).Mul(big.NewInt(int64(rub)*100), big.NewInt(secs))
	return v.Quo(v, big.NewInt(int64(periodDays)*86400)).Int64()
}

// ceilRub rounds kopecks up to whole roubles, at least one when there is anything.
func ceilRub(kop int64) int {
	if kop <= 0 {
		return 0
	}
	return int((kop + 99) / 100)
}

// QuotePurchase prices a purchase for the user right now. For a plan it is
// QuotePlanFor with the devices chosen; a change, devices and traffic are priced
// for the term the user has, with no period or code discount.
func (m *Manager) QuotePurchase(u model.User, p Purchase) (PlanQuote, error) {
	q, _, err := m.quotePurchase(u, p, time.Now().Unix())
	return q, err
}

// quotePurchase is QuotePurchase that also returns the plan it names.
func (m *Manager) quotePurchase(u model.User, p Purchase, now int64) (PlanQuote, *model.TariffPlan, error) {
	set, err := m.Settings()
	if err != nil {
		return PlanQuote{}, nil, err
	}
	if !set.BillingEnabled && p.Kind != "" && p.Kind != model.OrderPlan {
		return PlanQuote{}, nil, invalidCode("err.billingOff", "оплата выключена")
	}
	switch p.Kind {
	case "", model.OrderPlan:
		plan, err := m.store.GetTariffPlan(p.PlanID)
		if err != nil {
			return PlanQuote{}, nil, invalidCode("err.planNotFound", "тариф не найден")
		}
		devices, err := wantDevices(u, plan, p.Devices)
		if err != nil {
			return PlanQuote{}, nil, err
		}
		q := m.quotePlan(set, u, plan, max(p.Periods, 1), devices)
		return q, plan, nil
	case model.OrderChange:
		return m.quoteChange(set, u, p, now)
	case model.OrderDevices, model.OrderTraffic:
		return m.quoteAddon(set, u, p, now)
	}
	return PlanQuote{}, nil, invalidCode("err.purchaseUnknown", "неизвестная покупка")
}

// activeTerm is the user's active paid plan with a term still running, or an error
// saying why an add-on or a change has nothing to go onto.
func (m *Manager) activeTerm(u model.User, now int64) (*model.TariffPlan, error) {
	cur := m.ActivePaidPlan(u)
	if cur == nil {
		return nil, invalidCode("err.noActivePlan", "нет активной подписки")
	}
	if cur.PeriodDays <= 0 || u.ExpireAt <= now {
		return nil, invalidCode("err.planLifetimeNoAddons", "к бессрочному тарифу докупить ничего нельзя")
	}
	return cur, nil
}

// quoteAddon prices devices for the rest of the term, or a traffic pack.
func (m *Manager) quoteAddon(set *model.Settings, u model.User, p Purchase, now int64) (PlanQuote, *model.TariffPlan, error) {
	cur := m.ActivePaidPlan(u)
	if cur == nil {
		return PlanQuote{}, nil, invalidCode("err.noActivePlan", "нет активной подписки")
	}
	q := PlanQuote{Kind: p.Kind, Periods: 1, ExpireAt: u.ExpireAt}
	if p.Kind == model.OrderDevices {
		if _, err := m.activeTerm(u, now); err != nil {
			return PlanQuote{}, nil, err
		}
		if !cur.SellsDevices() {
			return PlanQuote{}, nil, invalidCode("err.devicesNotSold", "этот тариф не продаёт дополнительные устройства")
		}
		held := heldDevices(u, cur)
		if p.Devices < 1 || held+p.Devices > cur.DeviceMax {
			return PlanQuote{}, nil, invalidCode("err.devicesTooMany", "можно добавить не больше {{max}} устройств",
				map[string]any{"max": cur.DeviceMax - held})
		}
		q.Devices = p.Devices
		q.PriceRub = max(ceilRub(termValueKop(p.Devices*cur.DevicePrice, cur.PeriodDays, u.ExpireAt-now)), 1)
	} else {
		if cur.DataLimit <= 0 || u.DataLimit <= 0 {
			return PlanQuote{}, nil, invalidCode("err.noQuota", "у тарифа нет лимита трафика — докупать нечего")
		}
		if p.Pack < 0 || p.Pack >= len(set.TrafficPacks) {
			return PlanQuote{}, nil, invalidCode("err.packNotFound", "такого пакета нет")
		}
		pack := set.TrafficPacks[p.Pack]
		q.PackGB = pack.GB
		q.PriceRub = pack.PriceRub
	}
	q.TotalRub = q.PriceRub
	m.splitBalance(set, u, &q)
	return q, cur, nil
}

// quoteChange prices moving the active plan to another for the rest of its term.
func (m *Manager) quoteChange(set *model.Settings, u model.User, p Purchase, now int64) (PlanQuote, *model.TariffPlan, error) {
	if !set.PlanChange {
		return PlanQuote{}, nil, invalidCode("err.planChangeOff", "смена тарифа отключена — дождитесь конца срока или отмените подписку")
	}
	cur, err := m.activeTerm(u, now)
	if err != nil {
		return PlanQuote{}, nil, err
	}
	to, err := m.store.GetTariffPlan(p.PlanID)
	if err != nil {
		return PlanQuote{}, nil, invalidCode("err.planNotFound", "тариф не найден")
	}
	switch {
	case to.ID == cur.ID:
		return PlanQuote{}, nil, invalidCode("err.planChangeSame", "это ваш текущий тариф — продлите его")
	case !to.Enabled:
		return PlanQuote{}, nil, invalidCode("err.planUnavailable", "тариф недоступен")
	case to.IsFree():
		return PlanQuote{}, nil, invalidCode("err.planIsFree", "этот тариф бесплатный")
	case to.PeriodDays <= 0:
		return PlanQuote{}, nil, invalidCode("err.planChangeLifetime", "на бессрочный тариф нельзя перейти с доплатой — купите его после окончания срока")
	}
	devices := p.Devices
	if devices == KeepDevices {
		devices = 0
		if to.SellsDevices() {
			devices = min(heldDevices(u, cur), to.DeviceMax)
		}
	}
	if devices, err = wantDevices(u, to, devices); err != nil {
		return PlanQuote{}, nil, err
	}
	left := u.ExpireAt - now
	have := termValueKop(periodRub(cur, heldDevices(u, cur)), cur.PeriodDays, left)
	want := termValueKop(periodRub(to, devices), to.PeriodDays, left)
	q := PlanQuote{Kind: model.OrderChange, Periods: 1, Devices: devices, ExpireAt: u.ExpireAt}
	if want > have {
		// Dearer: the end date stays, the difference for the days left is paid.
		q.Upgrade = true
		q.PriceRub = max(ceilRub(want-have), 1)
		q.TotalRub = q.PriceRub
		m.splitBalance(set, u, &q)
		return q, to, nil
	}
	// Cheaper or the same: what the days left are worth, in days of the new plan.
	secs := new(big.Int).Mul(big.NewInt(have), big.NewInt(int64(to.PeriodDays)*86400))
	secs.Quo(secs, big.NewInt(int64(periodRub(to, devices))*100))
	q.ExpireAt = now + max(secs.Int64(), left)
	return q, to, nil
}

// splitBalance fills in the part of q.TotalRub the balance covers and what is left
// to pay with money, the way a plan's price is split.
func (m *Manager) splitBalance(set *model.Settings, u model.User, q *PlanQuote) {
	q.MoneyRub = q.TotalRub
	if !set.WalletEnabled || q.TotalRub == 0 {
		return
	}
	w, err := m.store.GetWalletLite(u.ID)
	if err != nil || w.BalanceKop <= 0 {
		return
	}
	total := int64(q.TotalRub) * 100
	if w.BalanceKop >= total {
		q.BalanceKop, q.MoneyRub = total, 0
		return
	}
	q.MoneyRub = int((total - w.BalanceKop + 99) / 100)
	q.BalanceKop = total - int64(q.MoneyRub)*100
}

// purchaseSubject names a purchase for the payer and the operator.
func purchaseSubject(lang i18n.Lang, kind, planName string, periods, devices, gb int) string {
	switch kind {
	case model.OrderChange:
		return i18n.T(lang, "order.changeSubject", planName)
	case model.OrderDevices:
		return i18n.TN(lang, "order.devicesSubject", devices)
	case model.OrderTraffic:
		return i18n.T(lang, "order.trafficSubject", gb)
	}
	s := planSubject(lang, planName, periods)
	if devices > 0 {
		s += i18n.TN(lang, "order.withDevices", devices)
	}
	return s
}

// addonDraft is the order for a change or an add-on, priced for the term the user
// has now.
func (m *Manager) addonDraft(u model.User, p Purchase, q PlanQuote, plan *model.TariffPlan) store.OrderDraft {
	d := store.OrderDraft{
		UserID: u.ID, PlanID: plan.ID, Kind: p.Kind, AmountRub: q.MoneyRub, BalanceKop: q.BalanceKop,
		Periods: 1, Devices: q.Devices, ExpectExpire: u.ExpireAt,
	}
	switch p.Kind {
	case model.OrderChange:
		d.ChangeFrom = u.PlanID
	case model.OrderTraffic:
		d.PackBytes = int64(q.PackGB) << 30
		d.ExpectExpire = 0
	}
	return d
}

// purchaseDraft checks a purchase opened for payment and builds its order.
func (m *Manager) purchaseDraft(lang i18n.Lang, userID int64, p Purchase) (store.OrderDraft, string, error) {
	u, err := m.store.GetUser(userID)
	if err != nil {
		return store.OrderDraft{}, "", err
	}
	if p.Kind == "" || p.Kind == model.OrderPlan {
		plan, err := m.store.GetTariffPlan(p.PlanID)
		if err != nil {
			return store.OrderDraft{}, "", invalidCode("err.planNotFound", "тариф не найден")
		}
		if err := m.checkPlanPurchase(*u, plan); err != nil {
			return store.OrderDraft{}, "", err
		}
		if err := m.checkPeriods(plan, max(p.Periods, 1)); err != nil {
			return store.OrderDraft{}, "", err
		}
	}
	q, plan, err := m.quotePurchase(*u, p, time.Now().Unix())
	if err != nil {
		return store.OrderDraft{}, "", err
	}
	if q.MoneyRub == 0 {
		return store.OrderDraft{}, "", invalidCode("err.payFromBalance", "баланса хватает — оплатите тариф с баланса")
	}
	subject := purchaseSubject(lang, p.Kind, plan.Name, q.Periods, q.Devices, q.PackGB)
	if p.Kind == "" || p.Kind == model.OrderPlan {
		return store.OrderDraft{
			UserID: userID, PlanID: plan.ID, Kind: model.OrderPlan, AmountRub: q.MoneyRub,
			BalanceKop: q.BalanceKop, DiscountRub: q.DiscountRub, PromoID: q.PromoID, Periods: q.Periods,
			Devices: q.Devices,
		}, subject, nil
	}
	return m.addonDraft(*u, p, q, plan), subject, nil
}

// StartPurchase opens a provider payment for a purchase.
func (m *Manager) StartPurchase(ctx context.Context, lang i18n.Lang, userID int64, p Purchase, provider, returnURL string) (*model.PaymentOrder, error) {
	d, subject, err := m.purchaseDraft(lang, userID, p)
	if err != nil {
		return nil, err
	}
	return m.startProviderOrder(ctx, lang, d, provider, returnURL, func(id int64) string {
		return i18n.T(lang, "order.description", subject, id)
	})
}

// RequestPurchaseManual opens a manual order for a purchase and returns the payment
// instructions.
func (m *Manager) RequestPurchaseManual(ctx context.Context, lang i18n.Lang, userID int64, p Purchase) (*model.PaymentOrder, string, error) {
	if !m.ManualPayment() {
		return nil, "", invalidCode("err.payMethodUnavailable", "способ оплаты недоступен")
	}
	d, subject, err := m.purchaseDraft(lang, userID, p)
	if err != nil {
		return nil, "", err
	}
	set, _ := m.Settings()
	return m.manualOrder(ctx, lang, d, subject, set)
}

// BuyFromBalance buys a purchase with the balance alone (a cheaper plan change costs
// nothing and goes through here too). expectExpire is the expiry the offering screen
// saw — AnyExpiry for no check.
func (m *Manager) BuyFromBalance(ctx context.Context, userID int64, p Purchase, expectExpire int64) (*model.PaymentOrder, error) {
	if p.Kind == "" || p.Kind == model.OrderPlan {
		return m.buyPlanFromBalance(ctx, userID, p, expectExpire)
	}
	now := time.Now().Unix()
	m.applyPlanMu.Lock()
	u, err := m.store.GetUser(userID)
	if err != nil {
		m.applyPlanMu.Unlock()
		return nil, err
	}
	if expectExpire != AnyExpiry && u.ExpireAt != expectExpire {
		m.applyPlanMu.Unlock()
		return nil, invalidCode("err.purchaseStale", "подписка уже изменилась — обновите страницу и повторите")
	}
	q, plan, err := m.quotePurchase(*u, p, now)
	if err != nil {
		m.applyPlanMu.Unlock()
		return nil, err
	}
	if q.MoneyRub > 0 {
		m.applyPlanMu.Unlock()
		return nil, invalidCode("err.balanceShort", "на балансе недостаточно средств")
	}
	d := m.addonDraft(*u, p, q, plan)
	bp := store.BalancePurchase{
		UserID: u.ID, PlanID: plan.ID, PriceKop: q.BalanceKop, Kind: model.TxPurchase,
		Periods: 1, Now: now, Order: d,
	}
	groupsChanged := false
	if p.Kind == model.OrderChange {
		w, err := m.changeWrite(*u, plan.ID, q.Devices, q.ExpireAt)
		if err != nil {
			m.applyPlanMu.Unlock()
			return nil, err
		}
		bp.Plan = w
		groupsChanged = m.planGroupsChanged(w)
	} else {
		bp.Addon = addonWrite(d)
	}
	orderID, err := m.store.BuyFromBalance(bp)
	m.applyPlanMu.Unlock()
	switch {
	case errors.Is(err, store.ErrInsufficientBalance):
		return nil, invalidCode("err.balanceShort", "на балансе недостаточно средств")
	case errors.Is(err, store.ErrPlanStale):
		return nil, invalidCode("err.purchaseStale", "подписка уже изменилась — обновите страницу и повторите")
	case err != nil:
		return nil, err
	}
	m.afterPlanWrite(groupsChanged)
	order, err := m.store.GetPaymentOrder(orderID)
	if err != nil {
		return nil, err
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventPaymentPaid, orderAudit(order, model.BalanceProvider))
	adminLang := m.botLang()
	m.notifyAdminEvent(model.AdminEventPayment, i18n.T(adminLang, "notify.paidBalance",
		order.ID, escHTML(u.Name), escHTML(orderSubject(adminLang, order)), kopText(q.BalanceKop)))
	m.EmitWebhook(model.WebhookPaymentPaid, order)
	return order, nil
}

// changeWrite moves the user to plan to on the term ending at expire, held to the
// plan and term they have now.
func (m *Manager) changeWrite(u model.User, to int64, devices int, expire int64) (store.UserPlanWrite, error) {
	w, _, err := m.planWriteForDays(u, to, false, true, -1, devices)
	if err != nil {
		return w, err
	}
	w.ExpireAt = expire
	w.RequirePlan, w.RequireExpire = u.PlanID, u.ExpireAt
	return w, nil
}

// addonWrite is the add-on an order delivers.
func addonWrite(o store.OrderDraft) *store.AddonWrite {
	return &store.AddonWrite{
		UserID: o.UserID, PlanID: o.PlanID, AddData: o.PackBytes,
		AddDevices:    map[bool]int{true: o.Devices}[o.Kind == model.OrderDevices],
		RequireExpire: o.ExpectExpire,
	}
}

// addonOrderSpec fills in what a paid change, devices or traffic order delivers. The
// money always goes through the balance, so an order that no longer fits its term
// leaves it there rather than nowhere.
func (m *Manager) addonOrderSpec(u model.User, order *model.PaymentOrder, spec *store.ConfirmSpec) error {
	spec.CreditKop = int64(order.AmountRub) * 100
	spec.DebitKop = spec.CreditKop + order.BalanceKop
	spec.PromoID = 0
	if order.Kind == model.OrderChange {
		w, err := m.changeWrite(u, order.PlanID, order.Devices, order.ExpectExpire)
		if err != nil {
			return err
		}
		// Held to the term the price was for, not the one the user has now.
		w.RequirePlan, w.RequireExpire = order.ChangeFrom, order.ExpectExpire
		spec.Plan = &w
		return nil
	}
	spec.Addon = addonWrite(store.OrderDraft{
		UserID: order.UserID, PlanID: order.PlanID, Kind: order.Kind,
		Devices: order.Devices, ExpectExpire: order.ExpectExpire, PackBytes: order.PackBytes,
	})
	return nil
}

// isAddonKind reports whether an order is a change or an add-on.
func isAddonKind(kind string) bool {
	return kind == model.OrderChange || kind == model.OrderDevices || kind == model.OrderTraffic
}

// ChangeOffers lists the plans the user may move to now and what each move costs.
func (m *Manager) ChangeOffers(u model.User) []ChangeOffer {
	set, err := m.Settings()
	if err != nil || !set.BillingEnabled || !set.PlanChange {
		return nil
	}
	now := time.Now().Unix()
	if _, err := m.activeTerm(u, now); err != nil {
		return nil
	}
	plans, err := m.store.ListTariffPlans(false)
	if err != nil {
		return nil
	}
	var out []ChangeOffer
	for i := range plans {
		p := plans[i]
		if p.ID == u.PlanID || p.IsFree() || p.PeriodDays <= 0 {
			continue
		}
		q, _, err := m.quoteChange(set, u, Purchase{Kind: model.OrderChange, PlanID: p.ID, Devices: KeepDevices}, now)
		if err != nil {
			continue
		}
		out = append(out, ChangeOffer{Plan: p, Quote: q})
	}
	return out
}

// ChangeOffer is one plan the user may move to and what it costs.
type ChangeOffer struct {
	Plan  model.TariffPlan `json:"plan"`
	Quote PlanQuote        `json:"quote"`
}

// AddonOffers says what the user can add to the plan they hold: how many devices
// more and at what price each for the rest of the term, and the traffic packs.
type AddonOffers struct {
	DevicesMax  int                 `json:"devices_max"`  // 0 = none to add
	DevicePrice int                 `json:"device_price"` // one device for the rest of the term, roubles
	Packs       []model.TrafficPack `json:"packs"`
}

// Addons lists what the user can add now.
func (m *Manager) Addons(u model.User) AddonOffers {
	var out AddonOffers
	set, err := m.Settings()
	if err != nil || !set.BillingEnabled {
		return out
	}
	now := time.Now().Unix()
	cur, err := m.activeTerm(u, now)
	if err != nil {
		return out
	}
	if cur.SellsDevices() {
		if left := cur.DeviceMax - heldDevices(u, cur); left > 0 {
			out.DevicesMax = left
			out.DevicePrice = max(ceilRub(termValueKop(cur.DevicePrice, cur.PeriodDays, u.ExpireAt-now)), 1)
		}
	}
	if cur.DataLimit > 0 && u.DataLimit > 0 {
		out.Packs = set.TrafficPacks
	}
	return out
}

// Any reports whether there is anything to add.
func (a AddonOffers) Any() bool { return a.DevicesMax > 0 || len(a.Packs) > 0 }
