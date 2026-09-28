package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The wallet: balance top-ups, paying for plans from it (in full or in part), renewal
// from it, promo codes and the referral programme. The money rules:
//
//   - An order's AmountRub is always money that arrives from outside, so the revenue
//     reports count a rouble once — when it is paid in, not again when it is spent.
//   - A plan is paid from the balance first; only what the balance does not cover is
//     invoiced. On confirmation the invoiced money lands on the balance and the full
//     price comes back off it, in the transaction that grants the plan.
//   - A referral reward is paid for money, never for balance spending.

// Limits on the amounts an operator and a user can enter.
const (
	walletTopupMax    = 1_000_000 // roubles in one top-up
	walletAdjustMax   = 100_000_000_00
	promoValueMaxRub  = 1_000_000
	promoValueMaxDays = 3650
	refDaysMax        = 365
	// renewAhead is how long before the end of a paid period the balance renews it.
	// Renewal extends from the current expiry, so renewing early costs the user nothing.
	renewAhead = time.Hour
	// renewGrace is how long after the end a period may still renew itself. It only
	// matters with no free plan configured: otherwise EnforceBilling moves an expired
	// user to the free plan in the same pass. Past it the plan is over, and money
	// arriving later is the user's to spend on whatever they choose.
	renewGrace = 24 * time.Hour
	// maxPendingTopups caps the top-up orders one user may have waiting: each one is
	// an invoice at the provider or an admin alert.
	maxPendingTopups   = 3
	topupPendingWindow = time.Hour
	// promoHoldWindow is how long a pending discounted order keeps a use of a limited
	// code spoken for — as long as a provider invoice lives before the sweep.
	promoHoldWindow = 24 * time.Hour
)

// PlanQuote is what buying a plan costs this user right now.
type PlanQuote struct {
	// Periods is how many of the plan's periods it buys; PeriodDiscountRub what buying
	// them together takes off the plain sum.
	Periods           int    `json:"periods"`
	PeriodPercent     int    `json:"period_percent"`
	PeriodDiscountRub int    `json:"period_discount_rub"`
	PriceRub          int    `json:"price_rub"`    // the plan's price × periods
	DiscountRub       int    `json:"discount_rub"` // what the attached promo code takes off
	PromoID           int64  `json:"promo_id,omitempty"`
	PromoCode         string `json:"promo_code,omitempty"`
	TotalRub          int    `json:"total_rub"`   // price after the discount
	BalanceKop        int64  `json:"balance_kop"` // the part the balance covers
	MoneyRub          int    `json:"money_rub"`   // the part to pay with money (0 = the balance covers it all)
}

// QuotePlan prices a plan for a user: the attached discount code (when it applies to
// this plan), then the balance, then what is left to pay.
func (m *Manager) QuotePlan(u model.User, plan *model.TariffPlan) PlanQuote {
	return m.QuotePlanFor(u, plan, 1)
}

// QuotePlanFor prices several of a plan's periods bought at once: the discount for
// the number of periods, then the user's discount code, then the balance. A number
// the operator offers no discount for is priced as one period.
func (m *Manager) QuotePlanFor(u model.User, plan *model.TariffPlan, periods int) PlanQuote {
	set, err := m.Settings()
	if err != nil {
		return PlanQuote{Periods: 1, PriceRub: plan.PriceRub, TotalRub: plan.PriceRub, MoneyRub: plan.PriceRub}
	}
	pct, ok := periodPercent(set, plan, periods)
	if !ok {
		periods, pct = 1, 0
	}
	base := plan.PriceRub * periods
	q := PlanQuote{Periods: periods, PeriodPercent: pct, PriceRub: base}
	q.PeriodDiscountRub = base * pct / 100
	q.TotalRub = base - q.PeriodDiscountRub
	w, err := m.store.GetWalletLite(u.ID)
	if err != nil {
		q.MoneyRub = q.TotalRub
		return q
	}
	if w.PromoID != 0 {
		if p, err := m.store.GetPromo(w.PromoID); err == nil && m.discountUsable(p, u.ID, plan.ID) == nil {
			q.DiscountRub = p.Discount(q.TotalRub)
			q.PromoID, q.PromoCode = p.ID, p.Code
			q.TotalRub -= q.DiscountRub
		}
	}
	total := int64(q.TotalRub) * 100
	if set.WalletEnabled && w.BalanceKop > 0 {
		if w.BalanceKop >= total {
			q.BalanceKop = total
			return q
		}
		// Round the money part UP to a whole rouble — providers take roubles — and let
		// the balance cover the rest, so a kopeck remainder stays on it.
		q.MoneyRub = int((total - w.BalanceKop + 99) / 100)
		q.BalanceKop = total - int64(q.MoneyRub)*100
		return q
	}
	q.MoneyRub = q.TotalRub
	return q
}

// periodPercent is the discount for buying n of a plan's periods: 0 for one, the
// operator's offer for more — and not ok when there is no such offer or the plan has
// no term to multiply.
func periodPercent(set *model.Settings, plan *model.TariffPlan, n int) (int, bool) {
	if n <= 1 {
		return 0, n == 1
	}
	if plan.PeriodDays <= 0 {
		return 0, false
	}
	for _, o := range set.BillingPeriods {
		if o.Periods == n {
			return o.Percent, true
		}
	}
	return 0, false
}

// PeriodOffers lists what buying several of the plan's periods costs this user: one
// quote per offer, none for a plan without a term.
func (m *Manager) PeriodOffers(u model.User, plan *model.TariffPlan) []PlanQuote {
	set, err := m.Settings()
	if err != nil || plan.PeriodDays <= 0 || len(set.BillingPeriods) == 0 {
		return nil
	}
	out := []PlanQuote{m.QuotePlanFor(u, plan, 1)}
	for _, o := range set.BillingPeriods {
		out = append(out, m.QuotePlanFor(u, plan, o.Periods))
	}
	return out
}

// checkPeriods refuses a number of periods the operator does not sell.
func (m *Manager) checkPeriods(plan *model.TariffPlan, periods int) error {
	set, err := m.Settings()
	if err != nil {
		return err
	}
	if _, ok := periodPercent(set, plan, periods); !ok {
		return invalidCode("err.periodsUnavailable", "такого срока нет")
	}
	return nil
}

// checkPlanPurchase is the rule every way of buying a plan shares: a paid plan, on
// sale (or the one the user already has — renewing a retired plan is allowed), and no
// switching away from an active paid plan.
func (m *Manager) checkPlanPurchase(u model.User, plan *model.TariffPlan) error {
	if plan.IsFree() {
		return invalidCode("err.planIsFree", "этот тариф бесплатный")
	}
	// A lifetime plan the user already holds has nothing left to buy: paying again
	// would take the money and change nothing.
	if cur := m.ActivePaidPlan(u); cur != nil && cur.ID == plan.ID && cur.PeriodDays <= 0 {
		return invalidCode("err.planLifetimeOwned", "этот тариф у вас уже бессрочный")
	}
	if u.PlanID != plan.ID {
		if !plan.Enabled {
			return invalidCode("err.planUnavailable", "тариф недоступен")
		}
		if cur := m.ActivePaidPlan(u); cur != nil {
			return invalidCode("err.activeSubscription", "у вас активна подписка «{{plan}}» — сначала отмените её, чтобы сменить тариф", map[string]any{"plan": cur.Name})
		}
	}
	return nil
}

// withBankedDays adds the user's banked referral days to a paid period being bought,
// returning how many it used (0 when the write has no term to lengthen).
func (m *Manager) withBankedDays(w *store.UserPlanWrite, userID int64) int {
	if w.ExpireAt <= 0 {
		return 0
	}
	wal, err := m.store.GetWalletLite(userID)
	if err != nil || wal.RefBonusDays <= 0 {
		return 0
	}
	w.ExpireAt += int64(wal.RefBonusDays) * 86400
	return wal.RefBonusDays
}

// refReward is the referral programme as the settings have it now.
func refReward(set *model.Settings) store.RefReward {
	if set == nil {
		return store.RefReward{}
	}
	return store.RefReward{
		Mode:      set.RefMode,
		Percent:   set.RefPercent,
		Days:      set.RefDays,
		FirstOnly: set.RefFirstOnly,
	}
}

// AnyExpiry is BuyPlanFromBalance's expectExpire for a caller with no screen to
// have gone stale (the external API): no check.
const AnyExpiry int64 = -1

// BuyPlanFromBalance buys a plan with the balance alone (after any discount). Fails
// when the balance does not cover it — the caller then offers a payment instead.
//
// expectExpire is the user's expiry as the screen that offered the purchase saw it.
// A second tap on the same button (Telegram delivers both) finds the expiry already
// moved by the first and is refused, instead of buying the next period too.
func (m *Manager) BuyPlanFromBalance(ctx context.Context, userID, planID, expectExpire int64, periods int) (*model.PaymentOrder, error) {
	set, err := m.Settings()
	if err != nil {
		return nil, err
	}
	if !set.BillingEnabled {
		return nil, invalidCode("err.billingOff", "оплата выключена")
	}
	plan, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return nil, invalidCode("err.planNotFound", "тариф не найден")
	}
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
	if err := m.checkPlanPurchase(*u, plan); err != nil {
		m.applyPlanMu.Unlock()
		return nil, err
	}
	if err := m.checkPeriods(plan, periods); err != nil {
		m.applyPlanMu.Unlock()
		return nil, err
	}
	q := m.QuotePlanFor(*u, plan, periods)
	if q.MoneyRub > 0 {
		m.applyPlanMu.Unlock()
		return nil, invalidCode("err.balanceShort", "на балансе недостаточно средств")
	}
	w, _, err := m.planWriteForPeriods(*u, plan.ID, m.isPlanRenewalFor(*u, plan.ID), true, periods)
	if err != nil {
		m.applyPlanMu.Unlock()
		return nil, err
	}
	bonus := m.withBankedDays(&w, u.ID)
	groupsChanged := m.planGroupsChanged(w)
	orderID, err := m.store.BuyFromBalance(store.BalancePurchase{
		UserID: u.ID, PlanID: plan.ID, PriceKop: q.BalanceKop,
		DiscountRub: q.DiscountRub, PromoID: q.PromoID,
		Plan: w, BonusDays: bonus, Kind: model.TxPurchase, Periods: periods, Now: time.Now().Unix(),
	})
	m.applyPlanMu.Unlock()
	if errors.Is(err, store.ErrInsufficientBalance) {
		return nil, invalidCode("err.balanceShort", "на балансе недостаточно средств")
	}
	if errors.Is(err, store.ErrPromoUsed) || errors.Is(err, store.ErrPromoUnavailable) {
		return nil, promoStoreErr(err)
	}
	if err != nil {
		return nil, err
	}
	m.supersedePromoOrders(ctx, u.ID, q.PromoID, orderID)
	m.afterPlanWrite(groupsChanged)
	order, err := m.store.GetPaymentOrder(orderID)
	if err != nil {
		return nil, err
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventPaymentPaid, map[string]any{
		"order_id": order.ID, "plan": plan.Name, "amount_rub": 0,
		"balance_kop": q.BalanceKop, "provider": model.BalanceProvider,
	})
	adminLang := m.botLang()
	m.notifyAdminEvent(model.AdminEventPayment, i18n.T(adminLang, "notify.paidBalance",
		order.ID, escHTML(u.Name), escHTML(plan.Name), kopText(q.BalanceKop)))
	m.EmitWebhook(model.WebhookPaymentPaid, order)
	return order, nil
}

// supersedePromoOrders cancels the user's other pending orders carrying the discount
// code an order or purchase just took: a code serves one checkout at a time.
func (m *Manager) supersedePromoOrders(ctx context.Context, userID, promoID, keep int64) {
	if promoID == 0 {
		return
	}
	cancelled, err := m.store.CancelPendingOrdersWithPromo(userID, promoID, keep)
	if err != nil {
		logErr("billing: supersede promo orders", "user", userID, "err", err)
		return
	}
	for _, o := range cancelled {
		m.audit(ctx, o.UserID, model.EventPaymentCancelled, map[string]any{
			"order_id": o.ID, "plan": o.PlanName, "amount_rub": o.AmountRub, "reason": "superseded",
		})
		m.EmitWebhook(model.WebhookPaymentCancelled, o)
	}
}

// StartTopup opens a provider payment that puts amountRub on the balance.
func (m *Manager) StartTopup(ctx context.Context, lang i18n.Lang, userID int64, amountRub int, provider, returnURL string) (*model.PaymentOrder, error) {
	if err := m.checkTopup(userID, amountRub); err != nil {
		return nil, err
	}
	return m.startProviderOrder(ctx, lang, topupDraft(userID, amountRub), provider, returnURL, func(id int64) string {
		return i18n.T(lang, "order.topupDescription", id)
	})
}

// RequestTopupManual opens a manual top-up order and returns the payment instructions.
func (m *Manager) RequestTopupManual(ctx context.Context, lang i18n.Lang, userID int64, amountRub int) (*model.PaymentOrder, string, error) {
	if !m.ManualPayment() {
		return nil, "", invalidCode("err.payMethodUnavailable", "способ оплаты недоступен")
	}
	if err := m.checkTopup(userID, amountRub); err != nil {
		return nil, "", err
	}
	set, _ := m.Settings()
	return m.manualOrder(ctx, lang, topupDraft(userID, amountRub), i18n.T(lang, "order.topupSubject"), set)
}

func (m *Manager) checkTopup(userID int64, amountRub int) error {
	set, err := m.Settings()
	if err != nil {
		return err
	}
	if !set.BillingEnabled || !set.WalletEnabled {
		return invalidCode("err.walletOff", "баланс выключен")
	}
	lo := max(set.WalletTopupMin, 1)
	if amountRub < lo || amountRub > walletTopupMax {
		return invalidCode("err.topupRange", "сумма пополнения: от {{min}} до {{max}} ₽",
			map[string]any{"min": lo, "max": walletTopupMax})
	}
	return nil
}

// topupDraft is a top-up order, capped at maxPendingTopups waiting within
// topupPendingWindow — enforced in the insert, so parallel requests cannot all pass.
// A manual one is never swept, so only recent ones count: a user who opened three and
// paid none is not locked out for good.
func topupDraft(userID int64, amountRub int) store.OrderDraft {
	return store.OrderDraft{
		UserID: userID, Kind: model.OrderTopup, AmountRub: amountRub,
		PendingCap: maxPendingTopups, PendingSince: time.Now().Add(-topupPendingWindow).Unix(),
	}
}

// orderCreateErr turns the store's refusals into what the user reads.
func orderCreateErr(err error) error {
	if errors.Is(err, store.ErrTooManyPending) {
		return invalidCode("err.topupPending", "у вас уже есть неоплаченные пополнения — оплатите их или подождите")
	}
	return err
}

// Wallet returns a user's balance and referral standing.
func (m *Manager) Wallet(userID int64) (model.Wallet, error) {
	return m.store.GetWallet(userID)
}

// BalanceHistory is a user's newest ledger lines.
func (m *Manager) BalanceHistory(userID int64, limit int) ([]model.BalanceTx, error) {
	return m.store.ListBalanceTx(userID, limit)
}

// SetAutoRenew turns renewal from the balance on or off for a user.
func (m *Manager) SetAutoRenew(ctx context.Context, userID int64, on bool) error {
	u, err := m.store.GetUser(userID)
	if err != nil {
		return err
	}
	if w, err := m.store.GetWalletLite(userID); err == nil && w.AutoRenew == on {
		return nil // nothing changes: no write, no journal line
	}
	if err := m.store.SetAutoRenew(userID, on); err != nil {
		return err
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventAutoRenew, map[string]any{"on": on})
	return nil
}

// AdjustBalance is an operator's correction of a balance, in kopecks of either sign.
func (m *Manager) AdjustBalance(ctx context.Context, userID, deltaKop int64, note string) (int64, error) {
	note = strings.TrimSpace(note)
	if deltaKop == 0 || deltaKop > walletAdjustMax || deltaKop < -walletAdjustMax {
		return 0, invalidCode("err.balanceAmount", "укажите сумму")
	}
	if len([]rune(note)) > 200 {
		return 0, invalidCode("err.balanceNote", "комментарий: не длиннее 200 символов")
	}
	u, err := m.store.GetUser(userID)
	if err != nil {
		return 0, err
	}
	bal, err := m.store.AdjustBalance(userID, deltaKop, note, time.Now().Unix())
	if errors.Is(err, store.ErrInsufficientBalance) {
		return 0, invalidCode("err.balanceNegative", "баланс не может стать отрицательным")
	}
	if err != nil {
		return 0, err
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventBalanceAdjusted, map[string]any{
		"amount_kop": deltaKop, "balance_kop": bal, "note": note,
	})
	if deltaKop > 0 {
		if set, err := m.Settings(); err == nil {
			m.notifyUserEvent(set, *u, model.UserNotifyPayment,
				i18n.T(m.userLang(u.TgChatID), "notify.userBalanceCredited", kopText(deltaKop), kopText(bal)))
		}
	}
	return bal, nil
}

// validateWalletSettings checks the wallet and referral settings before they are saved.
func validateWalletSettings(st *model.Settings) error {
	if st.WalletTopupMin < 1 || st.WalletTopupMin > walletTopupMax {
		return invalidCode("err.topupMinRange", "минимальное пополнение: от 1 до {{max}} ₽", map[string]any{"max": walletTopupMax})
	}
	switch st.RefMode {
	case "", model.RefOff:
		st.RefMode = model.RefOff
	case model.RefPercent:
		if !st.WalletEnabled {
			return invalidCode("err.refNeedsWallet", "процент от оплат начисляется на баланс — включите баланс")
		}
	case model.RefDays:
	default:
		return invalidCode("err.refMode", "неизвестный режим реферальной программы")
	}
	// Only the value the mode uses is the operator's to get right; the other keeps a
	// sane default for when the mode is switched to it.
	switch {
	case st.RefMode == model.RefPercent && (st.RefPercent < 1 || st.RefPercent > 100):
		return invalidCode("err.refPercentRange", "процент: от 1 до 100")
	case st.RefMode == model.RefDays && (st.RefDays < 1 || st.RefDays > refDaysMax):
		return invalidCode("err.refDaysRange", "дни: от 1 до {{max}}", map[string]any{"max": refDaysMax})
	}
	seen := map[int]bool{}
	for _, o := range st.BillingPeriods {
		if o.Periods < 2 || o.Periods > 36 || o.Percent < 0 || o.Percent > 90 || seen[o.Periods] {
			return invalidCode("err.periodOffer", "срок: от 2 до 36 периодов, скидка от 0 до 90%, без повторов")
		}
		seen[o.Periods] = true
	}
	slices.SortFunc(st.BillingPeriods, func(a, b model.PeriodOffer) int { return a.Periods - b.Periods })
	if wb := st.Winback; wb.Enabled && (wb.AfterDays < 1 || wb.AfterDays > 365 ||
		wb.Percent < 1 || wb.Percent > 90 || wb.ValidDays < 1 || wb.ValidDays > 90) {
		return invalidCode("err.winbackRange", "возврат ушедших: через 1–365 дней, скидка 1–90%, код действует 1–90 дней")
	}
	if st.RefPercent < 1 || st.RefPercent > 100 {
		st.RefPercent = 10
	}
	if st.RefDays < 1 || st.RefDays > refDaysMax {
		st.RefDays = 7
	}
	return nil
}

// --- referrals ---

// refCodeAlphabet leaves out look-alikes (0/o, 1/l/i), since people retype links.
const refCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func newRefCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = refCodeAlphabet[int(b[i])%len(refCodeAlphabet)]
	}
	return string(b)
}

// RefCode returns the user's invite code, minting it on first use. "" when the
// programme is off.
func (m *Manager) RefCode(userID int64) (string, error) {
	set, err := m.Settings()
	if err != nil {
		return "", err
	}
	if !set.RefEnabled() {
		return "", nil
	}
	return m.store.EnsureRefCode(userID, newRefCode)
}

// TrackReferral remembers that a chat that has no account yet arrived through an
// invite code.
func (m *Manager) TrackReferral(chatID int64, code string) {
	set, err := m.Settings()
	if err != nil || !set.RefEnabled() {
		return
	}
	id := m.store.UserIDByRefCode(code)
	if id == 0 {
		return
	}
	if err := m.store.SetSubscriberRef(chatID, id, time.Now().Unix()); err != nil {
		logErr("referral: remember invite failed", "chat", chatID, "err", err)
	}
}

// AttachReferrer makes the referrer a freshly registered chat arrived with the new
// account's referrer, and tells the referrer.
func (m *Manager) AttachReferrer(ctx context.Context, userID, chatID int64) {
	if chatID == 0 {
		return
	}
	ref, err := m.store.AttachReferrerFromChat(userID, chatID)
	if err != nil {
		logErr("referral: attach failed", "user", userID, "err", err)
		return
	}
	if ref == 0 {
		return
	}
	u, err := m.store.GetUser(userID)
	if err != nil {
		return
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventUserReferred, map[string]any{"referrer_id": ref})
	if r, err := m.store.GetUser(ref); err == nil {
		if set, err := m.Settings(); err == nil {
			m.notifyUserEvent(set, *r, model.UserNotifyPayment,
				i18n.T(m.userLang(r.TgChatID), "notify.refJoined", escHTML(u.Name)))
		}
	}
}

// notifyReferral tells a referrer what an invited user's payment earned them.
func (m *Manager) notifyReferral(set *model.Settings, res store.ConfirmResult) {
	if res.RefUserID == 0 {
		return
	}
	r, err := m.store.GetUser(res.RefUserID)
	if err != nil {
		return
	}
	lang := m.userLang(r.TgChatID)
	var msg string
	switch {
	case res.RefKop > 0:
		msg = i18n.T(lang, "notify.refEarnedMoney", kopText(res.RefKop))
	case res.RefBanked:
		msg = i18n.T(lang, "notify.refEarnedBanked", i18n.TN(lang, "sub.periodDays", res.RefDays))
	case res.RefDays > 0:
		msg = i18n.T(lang, "notify.refEarnedDays", i18n.TN(lang, "sub.periodDays", res.RefDays))
	default:
		return
	}
	m.notifyUserEvent(set, *r, model.UserNotifyPayment, msg)
}

// --- renewal from the balance ---

// RenewalOutlook is what renewal from the balance will do at the end of the user's
// term: ok when the balance covers it. Zero (PriceKop 0) when it will not be tried —
// no wallet, renewal off, or a plan renewal cannot extend.
type RenewalOutlook struct {
	PriceKop   int64
	BalanceKop int64
	At         int64 // when: the end of the term
	On         bool  // the user has renewal on; off, the rest says what turning it on would do
}

// Covered reports whether the balance pays for the renewal.
func (r RenewalOutlook) Covered() bool { return r.BalanceKop >= r.PriceKop }

// Renewal describes the coming renewal from the balance, if there is one to come.
func (m *Manager) Renewal(set *model.Settings, u model.User) RenewalOutlook {
	// Only what autoRenew will actually do: a disabled user is skipped, and a term
	// that ended longer ago than the grace is past renewing.
	if set == nil || !set.BillingEnabled || !set.WalletEnabled || !u.Enabled ||
		u.ExpireAt <= time.Now().Unix()-int64(renewGrace.Seconds()) {
		return RenewalOutlook{}
	}
	plan, err := m.store.GetTariffPlan(u.PlanID)
	if err != nil || plan.IsFree() || plan.PeriodDays <= 0 {
		return RenewalOutlook{}
	}
	w, err := m.store.GetWalletLite(u.ID)
	if err != nil {
		return RenewalOutlook{}
	}
	return RenewalOutlook{PriceKop: int64(plan.PriceRub) * 100, BalanceKop: w.BalanceKop, At: u.ExpireAt, On: w.AutoRenew}
}

// renewalNotice is the line the "expiring soon" message ends with: the balance will
// renew the plan, or how much it is short of that.
func (m *Manager) renewalNotice(set *model.Settings, u model.User, lang i18n.Lang) string {
	r := m.Renewal(set, u)
	switch {
	case r.PriceKop == 0 || !r.On:
		return ""
	case r.Covered():
		return "\n\n" + i18n.T(lang, "notify.renewCovered", kopText(r.PriceKop))
	default:
		return "\n\n" + i18n.T(lang, "notify.renewShort", kopText(r.BalanceKop), kopText(r.PriceKop-r.BalanceKop))
	}
}

// autoRenew renews, from the balance, every paid plan about to run out whose owner
// left renewal on and can afford it.
func (m *Manager) autoRenew(set *model.Settings, now int64) {
	users, err := m.store.UsersDueRenewal(now-int64(renewGrace.Seconds()), now+int64(renewAhead.Seconds()))
	if err != nil {
		logErr("billing: renewal candidates", "err", err)
		return
	}
	for _, u := range users {
		m.renewFromBalance(set, u.ID, now)
	}
}

func (m *Manager) renewFromBalance(set *model.Settings, userID, now int64) {
	m.applyPlanMu.Lock()
	u, err := m.store.GetUser(userID)
	if err != nil {
		m.applyPlanMu.Unlock()
		return
	}
	// Re-checked under the lock: the user may have renewed (or turned renewal off)
	// between the candidate query and their turn here.
	wal, err := m.store.GetWalletLite(u.ID)
	due := u.ExpireAt > now-int64(renewGrace.Seconds()) && u.ExpireAt <= now+int64(renewAhead.Seconds())
	if err != nil || !wal.AutoRenew || !due || !u.Enabled {
		m.applyPlanMu.Unlock()
		return
	}
	plan, err := m.store.GetTariffPlan(u.PlanID)
	if err != nil || plan.IsFree() || plan.PeriodDays <= 0 {
		m.applyPlanMu.Unlock()
		return
	}
	w, _, err := m.planWriteFor(*u, plan.ID, true, true)
	if err != nil {
		m.applyPlanMu.Unlock()
		return
	}
	bonus := m.withBankedDays(&w, u.ID)
	groupsChanged := m.planGroupsChanged(w)
	price := int64(plan.PriceRub) * 100
	orderID, err := m.store.BuyFromBalance(store.BalancePurchase{
		UserID: u.ID, PlanID: plan.ID, PriceKop: price,
		Plan: w, BonusDays: bonus, Kind: model.TxRenew, Now: now,
	})
	m.applyPlanMu.Unlock()
	if errors.Is(err, store.ErrInsufficientBalance) {
		return
	}
	if err != nil {
		logErr("billing: renewal from balance failed", "user", u.ID, "err", err)
		return
	}
	m.afterPlanWrite(groupsChanged)
	logInfo("billing: plan renewed from balance", "user", u.ID, "plan", plan.Name, "order", orderID)
	m.auditNamed(context.Background(), u.ID, u.Name, model.EventPlanRenewed, map[string]any{
		"order_id": orderID, "plan": plan.Name, "balance_kop": price, "expire_at": w.ExpireAt,
	})
	wal, _ = m.store.GetWalletLite(u.ID)
	lang := m.userLang(u.TgChatID)
	until := time.Unix(w.ExpireAt, 0).In(m.loc()).Format("02.01.2006")
	m.notifyUserEvent(set, *u, model.UserNotifyPayment,
		i18n.T(lang, "notify.userRenewed", escHTML(plan.Name), until, kopText(wal.BalanceKop)))
	if order, err := m.store.GetPaymentOrder(orderID); err == nil {
		m.EmitWebhook(model.WebhookPaymentPaid, order)
	}
}

// --- promo codes ---

// PromoResult is what entering a code did.
type PromoResult struct {
	Kind     string `json:"kind"`
	Value    int    `json:"value"`
	Code     string `json:"code"`
	PlanName string `json:"plan_name,omitempty"` // days: the plan the days went to
	ExpireAt int64  `json:"expire_at,omitempty"` // days: the new end of the term
}

// PromoMessage says what an entered code did, in the user's language. esc escapes
// the operator-chosen strings (the code, a plan name) for where the text goes — HTML
// for the bot, nothing for the subscription page, which sets it as plain text.
func PromoMessage(res *PromoResult, lang i18n.Lang, loc *time.Location, esc func(string) string) string {
	code := esc(res.Code)
	switch res.Kind {
	case model.PromoBalance:
		return i18n.T(lang, "user.promoBalance", code, res.Value)
	case model.PromoDays:
		until := ""
		if res.ExpireAt > 0 {
			until = time.Unix(res.ExpireAt, 0).In(loc).Format("02.01.2006")
		}
		return i18n.T(lang, "user.promoDays", code, i18n.TN(lang, "sub.periodDays", res.Value), esc(res.PlanName), until)
	case model.PromoPercent:
		return i18n.T(lang, "user.promoPercent", code, res.Value)
	default:
		return i18n.T(lang, "user.promoAmount", code, res.Value)
	}
}

// promoTries bounds how many codes one user may try — a hit is money, so guessing
// must cost something — and how many unknown codes everyone together may try, so
// the budget does not grow with the number of accounts an attacker registers.
type promoTries struct {
	mu     sync.Mutex
	hits   map[int64][]time.Time
	misses []time.Time
}

const (
	promoTryWindow = 10 * time.Minute
	promoTryMax    = 10
	promoMissMax   = 300
)

// missAllowed reports whether unknown codes may still be tried panel-wide.
func (p *promoTries) missAllowed(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.misses[:0]
	for _, t := range p.misses {
		if now.Sub(t) < promoTryWindow {
			kept = append(kept, t)
		}
	}
	p.misses = kept
	return len(kept) < promoMissMax
}

// miss records an unknown code.
func (p *promoTries) miss(now time.Time) {
	p.mu.Lock()
	p.misses = append(p.misses, now)
	p.mu.Unlock()
}

func (p *promoTries) allow(userID int64, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hits == nil {
		p.hits = map[int64][]time.Time{}
	}
	kept := p.hits[userID][:0]
	for _, t := range p.hits[userID] {
		if now.Sub(t) < promoTryWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= promoTryMax {
		p.hits[userID] = kept
		return false
	}
	p.hits[userID] = append(kept, now)
	// Drop idle users now and then so the map does not keep everyone who ever tried.
	if len(p.hits) > 4096 {
		for id, ts := range p.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= promoTryWindow {
				delete(p.hits, id)
			}
		}
	}
	return true
}

// promoOpen checks what every code needs: on, not expired, not used up, not used by
// this user yet.
func (m *Manager) promoOpen(p model.PromoCode, userID int64) error {
	now := time.Now().Unix()
	if !p.Enabled || (p.ExpiresAt > 0 && p.ExpiresAt <= now) || (p.MaxUses > 0 && p.Uses >= p.MaxUses) {
		return invalidCode("err.promoUnavailable", "промокод больше не действует")
	}
	// A discount is used at payment, so uses already promised to other users' open
	// checkouts count against the limit too.
	if model.IsDiscount(p.Kind) && p.MaxUses > 0 &&
		p.Uses+m.store.PromoPendingHolders(p.ID, userID, now-int64(promoHoldWindow.Seconds())) >= p.MaxUses {
		return invalidCode("err.promoUnavailable", "промокод больше не действует")
	}
	if m.store.PromoUsedBy(p.ID, userID) {
		return invalidCode("err.promoUsed", "вы уже использовали этот промокод")
	}
	return nil
}

// discountUsable reports why a discount code cannot pay for this plan (nil = it can).
func (m *Manager) discountUsable(p model.PromoCode, userID, planID int64) error {
	if !model.IsDiscount(p.Kind) {
		return invalidCode("err.promoUnavailable", "промокод больше не действует")
	}
	if err := m.promoOpen(p, userID); err != nil {
		return err
	}
	if !p.AppliesTo(planID) {
		return invalidCode("err.promoNotForPlan", "промокод не действует на этот тариф")
	}
	if p.FirstOnly {
		if paid, err := m.store.HasBoughtPlan(userID); err != nil || paid {
			return invalidCode("err.promoFirstOnly", "промокод только для первой покупки тарифа")
		}
	}
	return nil
}

// discountFitsAPlan reports whether a discount code would take money off any plan
// the user can buy now — the active plan's renewal, or any paid plan on sale when
// there is none. Accepting a code that fits nothing would promise a lower price
// the user never sees.
func (m *Manager) discountFitsAPlan(p model.PromoCode, u model.User) bool {
	if active := m.ActivePaidPlan(u); active != nil {
		return active.PeriodDays > 0 && p.AppliesTo(active.ID)
	}
	plans, err := m.store.ListTariffPlans(false)
	if err != nil {
		return false
	}
	for _, plan := range plans {
		if !plan.IsFree() && p.AppliesTo(plan.ID) {
			return true
		}
	}
	return false
}

// RedeemPromo applies a code a user entered: a balance code credits the balance, a
// days code extends or grants a plan, a discount code waits for the next payment.
func (m *Manager) RedeemPromo(ctx context.Context, userID int64, code string) (*PromoResult, error) {
	code = strings.TrimSpace(code)
	set, err := m.Settings()
	if err != nil {
		return nil, err
	}
	if !set.BillingEnabled {
		return nil, invalidCode("err.billingOff", "оплата выключена")
	}
	if !m.promoTry.allow(userID, time.Now()) {
		return nil, invalidCode("err.promoTooMany", "слишком много попыток — попробуйте позже")
	}
	code = strings.ToUpper(code)
	p, err := m.store.GetPromoByCode(code)
	if code == "" || len(code) > 64 || errors.Is(err, sql.ErrNoRows) {
		// The panel-wide budget only ever refuses unknown codes: junk from many
		// accounts must not lock out a user holding a real one.
		if !m.promoTry.missAllowed(time.Now()) {
			return nil, invalidCode("err.promoTooMany", "слишком много попыток — попробуйте позже")
		}
		m.promoTry.miss(time.Now())
		return nil, invalidCode("err.promoNotFound", "такого промокода нет")
	}
	if err != nil {
		return nil, err
	}
	u, err := m.store.GetUser(userID)
	if err != nil {
		return nil, err
	}
	if err := m.promoOpen(p, userID); err != nil {
		return nil, err
	}
	res := &PromoResult{Kind: p.Kind, Value: p.Value, Code: p.Code}
	now := time.Now().Unix()
	switch p.Kind {
	case model.PromoBalance:
		if !set.WalletEnabled {
			return nil, invalidCode("err.walletOff", "баланс выключен")
		}
		if err := m.store.RedeemPromoBalance(p.ID, userID, int64(p.Value)*100, now); err != nil {
			return nil, promoStoreErr(err)
		}
	case model.PromoDays:
		if err := m.redeemPromoDays(p, *u, now, res); err != nil {
			return nil, err
		}
	case model.PromoPercent, model.PromoAmount:
		if !m.discountFitsAPlan(p, *u) {
			return nil, invalidCode("err.promoNotForPlan", "промокод не действует на этот тариф")
		}
		if p.FirstOnly {
			if paid, err := m.store.HasBoughtPlan(userID); err != nil || paid {
				return nil, invalidCode("err.promoFirstOnly", "промокод только для первой покупки тарифа")
			}
		}
		if err := m.store.SetUserPromo(userID, p.ID); err != nil {
			return nil, err
		}
	default:
		return nil, invalidCode("err.promoUnavailable", "промокод больше не действует")
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventPromoRedeemed, map[string]any{
		"code": p.Code, "kind": p.Kind, "value": p.Value,
	})
	return res, nil
}

// redeemPromoDays grants a days code: onto the active paid plan (PlanID 0), or the
// code's own plan — extending it when the user already has it, starting it otherwise.
func (m *Manager) redeemPromoDays(p model.PromoCode, u model.User, now int64, res *PromoResult) error {
	m.applyPlanMu.Lock()
	defer m.applyPlanMu.Unlock()
	fresh, err := m.store.GetUser(u.ID)
	if err != nil {
		return err
	}
	u = *fresh
	active := m.ActivePaidPlan(u)
	planID := p.PlanID
	switch {
	case active != nil && active.PeriodDays <= 0:
		// A lifetime plan has no end to move: days would give it one.
		return invalidCode("err.promoLifetime", "ваша подписка бессрочная — дни ей не нужны")
	case planID == 0:
		if active == nil {
			return invalidCode("err.promoNeedsPlan", "промокод продлевает подписку — сначала оформите тариф")
		}
		planID = active.ID
	case active != nil && active.ID != planID:
		return invalidCode("err.activeSubscription", "у вас активна подписка «{{plan}}» — сначала отмените её, чтобы сменить тариф", map[string]any{"plan": active.Name})
	}
	// A plan without a term given "for N days" would never end.
	if plan, err := m.store.GetTariffPlan(planID); err != nil || plan.IsFree() || plan.PeriodDays <= 0 {
		return invalidCode("err.promoUnavailable", "промокод больше не действует")
	}
	// Days go onto a running paid plan; after a trial or a free plan they start now.
	w, name, err := m.planWriteForDays(u, planID, active != nil, false, p.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return invalidCode("err.promoUnavailable", "промокод больше не действует") // its plan was deleted
	}
	if err != nil {
		return err
	}
	groupsChanged := m.planGroupsChanged(w)
	if err := m.store.RedeemPromoPlan(p.ID, u.ID, w, now); err != nil {
		return promoStoreErr(err)
	}
	m.afterPlanWrite(groupsChanged)
	res.PlanName, res.ExpireAt = name, w.ExpireAt
	return nil
}

func promoStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrPromoUsed):
		return invalidCode("err.promoUsed", "вы уже использовали этот промокод")
	case errors.Is(err, store.ErrPromoUnavailable):
		return invalidCode("err.promoUnavailable", "промокод больше не действует")
	}
	return err
}

// ListPromos returns every promo code.
func (m *Manager) ListPromos() ([]model.PromoCode, error) { return m.store.ListPromos() }

// PromosOffered reports whether there is any code a user could enter.
func (m *Manager) PromosOffered() bool { return m.store.CountEnabledPromos() > 0 }

// SavePromo validates and stores a promo code.
func (m *Manager) SavePromo(p *model.PromoCode) error {
	// Codes are stored upper-case: the column's NOCASE folds only Latin letters, and
	// a Cyrillic code must match however the user typed it too.
	p.Code = strings.ToUpper(strings.TrimSpace(p.Code))
	p.Note = strings.TrimSpace(p.Note)
	if p.Code == "" || len(p.Code) > 64 || strings.ContainsAny(p.Code, " \t\r\n") {
		return invalidCode("err.promoCode", "код: без пробелов, до 64 символов")
	}
	switch p.Kind {
	case model.PromoPercent:
		if p.Value < 1 || p.Value > 100 {
			return invalidCode("err.promoPercent", "скидка: от 1 до 100%")
		}
	case model.PromoAmount, model.PromoBalance:
		if p.Value < 1 || p.Value > promoValueMaxRub {
			return invalidCode("err.promoRub", "сумма: от 1 до {{max}} ₽", map[string]any{"max": promoValueMaxRub})
		}
	case model.PromoDays:
		if p.Value < 1 || p.Value > promoValueMaxDays {
			return invalidCode("err.promoDays", "дни: от 1 до {{max}}", map[string]any{"max": promoValueMaxDays})
		}
	default:
		return invalidCode("err.promoKind", "неизвестный вид промокода")
	}
	if p.MaxUses < 0 || p.ExpiresAt < 0 {
		return invalidCode("err.promoLimits", "лимит и срок не могут быть отрицательными")
	}
	if len([]rune(p.Note)) > 200 {
		return invalidCode("err.promoNote", "заметка: не длиннее 200 символов")
	}
	if !model.IsDiscount(p.Kind) {
		p.PlanIDs, p.FirstOnly = []int64{}, false
	}
	if p.Kind != model.PromoDays {
		p.PlanID = 0
	}
	// A plan deleted since the code was made drops out of its list — but a list left
	// empty would mean "any plan", widening the code, so that is refused instead.
	kept := []int64{}
	for _, id := range p.PlanIDs {
		if _, err := m.store.GetTariffPlan(id); err == nil {
			kept = append(kept, id)
		}
	}
	if len(p.PlanIDs) > 0 && len(kept) == 0 {
		return invalidCode("err.planNotFound", "тариф не найден")
	}
	p.PlanIDs = kept
	if p.PlanID != 0 {
		plan, err := m.store.GetTariffPlan(p.PlanID)
		if err != nil {
			return invalidCode("err.planNotFound", "тариф не найден")
		}
		if plan.IsFree() || plan.PeriodDays <= 0 {
			return invalidCode("err.promoPaidPlan", "промокод на дни выдаёт только платный тариф со сроком")
		}
	}
	err := m.store.SavePromo(p, time.Now().Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return invalidCode("err.promoTaken", "такой код уже есть")
	}
	return err
}

// DeletePromo removes a promo code.
func (m *Manager) DeletePromo(id int64) error { return m.store.DeletePromo(id) }

// kopText renders kopecks as roubles.
func kopText(kop int64) string { return model.KopText(kop) }

// PeriodLabel describes one term of a plan for a buyer: "30 d — 199 ₽",
// "3 × 30 d — 537 ₽ (−10%)".
func PeriodLabel(lang i18n.Lang, plan *model.TariffPlan, q PlanQuote) string {
	days := i18n.TN(lang, "sub.periodDays", plan.PeriodDays)
	if q.Periods > 1 {
		days = fmt.Sprintf("%d × %s", q.Periods, days)
	}
	label := i18n.T(lang, "sub.periodOption", days, q.TotalRub)
	if q.PeriodPercent > 0 {
		label += " " + i18n.T(lang, "sub.periodSaved", q.PeriodPercent)
	}
	return label
}
