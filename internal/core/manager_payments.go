package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/payments"
	"github.com/AppsGanin/rospanel/internal/store"
)

// escHTML escapes a dynamic value for the bots' HTML parse mode.
func escHTML(s string) string { return html.EscapeString(s) }

// SetUserNotifier registers a callback (the user bot) that pushes a message to a
// VPN user's Telegram chat. Passing nil clears it.
func (m *Manager) SetUserNotifier(fn func(chatID int64, html string)) {
	m.notifyMu.Lock()
	m.userNotify = fn
	m.notifyMu.Unlock()
}

func (m *Manager) notifyUser(chatID int64, html string) {
	m.notifyMu.Lock()
	fn := m.userNotify
	m.notifyMu.Unlock()
	if fn != nil && chatID != 0 {
		fn(chatID, html)
	}
}

// SetAdminNotifier registers a callback (the admin bot) that broadcasts a message
// to all authorized admin chats. Passing nil clears it.
func (m *Manager) SetAdminNotifier(fn func(html string)) {
	m.notifyMu.Lock()
	m.adminNotify = fn
	m.notifyMu.Unlock()
}

// SetAdminModerationNotifier registers a callback (the admin bot) that posts a
// signup awaiting moderation, with approve/reject buttons. Passing nil clears it.
func (m *Manager) SetAdminModerationNotifier(fn func(reqID int64, name, plan string)) {
	m.notifyMu.Lock()
	m.adminModerate = fn
	m.notifyMu.Unlock()
}

// notifyModeration best-effort pings the admin bot about a pending request. It's not
// a delivery guarantee — the panel queue is the authoritative surface.
func (m *Manager) notifyModeration(reqID int64, name, plan string) {
	m.notifyMu.Lock()
	fn := m.adminModerate
	m.notifyMu.Unlock()
	if fn != nil {
		fn(reqID, name, plan)
	}
}

// methodLabel names a payment method in an alert. Provider names are brands and
// travel as-is; the one that needs wording is the manual path, which is a word and
// not a brand — so it comes from the dictionary.
func (m *Manager) methodLabel(lang i18n.Lang, key string) string {
	if key == "" || key == "manual" {
		return i18n.T(lang, "pay.manual")
	}
	return m.ProviderLabel(key)
}

// ProviderLabel is the pay-button label for a provider key: the operator's custom
// name if they set one, otherwise the provider's default. "" ⇒ a manual order.
func (m *Manager) ProviderLabel(key string) string {
	d, ok := payments.Get(key)
	if !ok {
		return payments.Label(key)
	}
	p, err := m.store.GetPaymentProvider(key)
	if err != nil {
		return d.Label
	}
	return d.DisplayName(p.Config)
}

// PaymentMethods returns the enabled, fully-configured provider keys, in registry
// order (which is the order the bot and the subscription page offer them in).
func (m *Manager) PaymentMethods() []string {
	saved, err := m.store.ListPaymentProviders()
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range payments.All() {
		p, ok := saved[d.Key]
		if ok && p.Enabled && d.Configured(p.Config) && (d.Key != payments.ProviderStars || m.userBotOn()) {
			out = append(out, d.Key)
		}
	}
	return out
}

// ManualPayment reports whether manual payment is offered: an order the user pays by
// transfer, from the operator's own instructions, and an admin confirms. It is a
// method beside the automatic providers, not a fallback for having none.
func (m *Manager) ManualPayment() bool {
	set, err := m.Settings()
	return err == nil && set.BillingManualEnabled
}

// ManualPaymentLabel is the manual method's pay-button label: the operator's own
// wording where they set one, the dictionary's otherwise — the same rule a provider's
// display name follows.
func (m *Manager) ManualPaymentLabel(lang i18n.Lang) string {
	if set, err := m.Settings(); err == nil {
		if l := strings.TrimSpace(set.BillingManualLabel); l != "" {
			return l
		}
	}
	return i18n.T(lang, "pay.manualMethod")
}

// PaymentProviders returns every provider in the registry paired with its saved
// setup (a provider the operator never configured comes back with an empty config).
func (m *Manager) PaymentProviders() ([]payments.Descriptor, map[string]model.PaymentProvider, error) {
	saved, err := m.store.ListPaymentProviders()
	if err != nil {
		return nil, nil, err
	}
	return payments.All(), saved, nil
}

// SavePaymentProvider validates and persists one provider's setup. A secret field
// left empty means "keep the stored value", so toggling a provider or editing its
// shop id never wipes its API key. Enabling a provider with a required field still
// missing is refused — a half-configured provider would just fail at checkout.
func (m *Manager) SavePaymentProvider(key string, enabled bool, cfg map[string]string) error {
	d, ok := payments.Get(key)
	if !ok {
		return invalidCode("err.unknownPayMethod", "неизвестный способ оплаты")
	}
	cur, err := m.store.GetPaymentProvider(key)
	if err != nil {
		return err
	}
	next := map[string]string{}
	for _, f := range d.Fields {
		v := strings.TrimSpace(cfg[f.Key])
		if f.Kind == payments.FieldSecret && v == "" {
			v = cur.Config[f.Key] // empty secret = keep current
		}
		if f.Kind == payments.FieldBool {
			v = boolValue(cfg[f.Key])
		}
		next[f.Key] = v
	}
	// The custom pay-button name is a universal optional field, not a credential;
	// keep it when provided, drop it when cleared (falls back to the default label).
	if dn := strings.TrimSpace(cfg[payments.DisplayNameKey]); dn != "" {
		next[payments.DisplayNameKey] = dn
	}
	if enabled {
		for _, f := range d.Fields {
			if f.Optional || f.Kind == payments.FieldBool || next[f.Key] != "" {
				continue
			}
			return invalidCode("err.providerFieldRequired", "{{provider}}: заполните «{{field}}»", map[string]any{"provider": d.Label, "field": f.Label})
		}
	}
	if err := m.store.SavePaymentProvider(model.PaymentProvider{Key: key, Enabled: enabled, Config: next}); err != nil {
		return err
	}
	return m.ensureWebhookSecret()
}

// boolValue normalises the several ways a toggle arrives over JSON.
func boolValue(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return "1"
	default:
		return ""
	}
}

// providerClient builds the API client for an enabled, configured provider.
func (m *Manager) providerClient(key string) (payments.Client, error) {
	d, ok := payments.Get(key)
	if !ok {
		return nil, invalidCode("err.unknownPayMethod", "неизвестный способ оплаты")
	}
	p, err := m.store.GetPaymentProvider(key)
	if err != nil {
		return nil, err
	}
	if !p.Enabled {
		return nil, invalidCode("err.providerDisabled", "{{provider}}: способ оплаты выключен", map[string]any{"provider": d.Label})
	}
	if !d.Configured(p.Config) {
		return nil, invalidCode("err.providerUnconfigured", "{{provider}}: не заполнены настройки", map[string]any{"provider": d.Label})
	}
	if key == payments.ProviderStars {
		// Stars are invoiced by the user bot, with its token and the Telegram route.
		set, err := m.Settings()
		if err != nil {
			return nil, err
		}
		if !set.TGUserBotEnabled || strings.TrimSpace(set.TGUserBotToken) == "" {
			return nil, invalidCode("err.starsNeedBot", "Telegram Stars принимает пользовательский бот — включите его")
		}
		cfg := payments.Config{}
		for k, v := range p.Config {
			cfg[k] = v
		}
		cfg[payments.StarsBotToken] = strings.TrimSpace(set.TGUserBotToken)
		cfg[payments.StarsProxy] = set.TelegramProxyURL()
		return d.New(cfg), nil
	}
	return d.New(p.Config), nil
}

// userBotOn reports whether the user bot runs — what Stars payments need.
func (m *Manager) userBotOn() bool {
	set, err := m.Settings()
	return err == nil && set.TGUserBotEnabled && strings.TrimSpace(set.TGUserBotToken) != ""
}

// StarsPreCheckout answers Telegram's "may this payment go ahead": the invoice must
// be one of ours, for an order not paid yet, asking the star count it is paid with.
func (m *Manager) StarsPreCheckout(payload, currency string, total int64) error {
	o, err := m.store.GetPaymentOrderByProvider(payments.ProviderStars, payload)
	if err != nil {
		return invalidCode("err.orderNotFound", "заказ не найден")
	}
	if o.Status == "paid" {
		return invalidCode("err.orderAlreadyPaid", "заказ уже оплачен")
	}
	if want, ok := payments.StarsPayloadAmount(payload); !ok || currency != "XTR" || total != want {
		return invalidCode("err.orderAmountChanged", "сумма заказа изменилась — начните оплату заново")
	}
	return nil
}

// ConfirmStarsPayment applies a Stars payment the user bot received. raw is the
// update as Telegram sent it — it lands in the callback journal, and its
// telegram_payment_charge_id is what a refund of the stars needs.
func (m *Manager) ConfirmStarsPayment(payload, currency string, total int64, raw []byte) error {
	rec := model.PaymentWebhook{
		At: time.Now().Unix(), Provider: payments.ProviderStars, RemoteIP: "telegram",
		ProviderID: payload, Status: string(payments.StatusPaid), Body: string(raw),
	}
	err := func() error {
		if want, ok := payments.StarsPayloadAmount(payload); !ok || currency != "XTR" || total != want {
			rec.Outcome = model.WebhookOutcomeMismatch
			if o, e := m.store.GetPaymentOrderByProvider(payments.ProviderStars, payload); e == nil {
				rec.OrderID = o.ID
			}
			return fmt.Errorf("stars payment %q: paid %d %s", payload, total, currency)
		}
		var err error
		// The star count was checked against the invoice above; the order's rouble
		// amount has no star figure to compare with, so none is passed on.
		rec.Outcome, rec.OrderID, err = m.confirmProviderOrderOutcome(payments.ProviderStars, payload,
			payments.Result{Status: payments.StatusPaid})
		return err
	}()
	if err != nil {
		rec.Error = err.Error()
	}
	if e := m.store.RecordPaymentWebhook(rec); e != nil {
		logErr("stars: journal write failed", "err", e)
	}
	if err != nil && rec.Outcome == model.WebhookOutcomeMismatch {
		m.notifyAdminEvent(model.AdminEventPayment, i18n.T(m.botLang(), "notify.starsMismatch",
			rec.OrderID, total, escHTML(payload)))
	}
	return err
}

func (m *Manager) ensureWebhookSecret() error {
	set, err := m.Settings()
	if err != nil {
		return err
	}
	if strings.TrimSpace(set.PaymentWebhookSecret) != "" {
		return nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	return m.store.SetPaymentWebhookSecret(hex.EncodeToString(b))
}

// PaymentWebhookSecret returns the random webhook URL segment (may be empty).
func (m *Manager) PaymentWebhookSecret() string {
	set, _ := m.Settings()
	if set == nil {
		return ""
	}
	return set.PaymentWebhookSecret
}

// PaymentWebhookURL is the public callback URL to paste into a provider's
// dashboard: /<random secret>/<provider key>. Empty when the panel doesn't know
// its own host yet, or before the secret has been generated.
func (m *Manager) PaymentWebhookURL(key string) string {
	if key == payments.ProviderStars {
		return "" // the bot hears about Stars payments; there is no callback to point
	}
	set, _ := m.Settings()
	if set == nil || set.PaymentWebhookSecret == "" || set.Host == "" {
		return ""
	}
	return "https://" + set.Host + "/" + set.PaymentWebhookSecret + "/" + key
}

// StartPlanPayment creates an order plus a provider payment and returns the order
// with its hosted pay URL. provider may be "" when exactly one method is enabled.
// The payer is returned to Telegram after paying (the bot flow).
func (m *Manager) StartPlanPayment(ctx context.Context, lang i18n.Lang, userID, planID int64, provider string, periods int) (*model.PaymentOrder, error) {
	return m.startPlanPayment(ctx, lang, userID, planID, provider, "https://t.me/", periods)
}

// StartPlanPaymentReturn is StartPlanPayment for the web subscription page: it
// sends the payer back to returnURL (the sub page) after a card payment instead of
// to Telegram. returnURL is used by hosted-form providers (YooKassa); CryptoBot
// ignores it.
func (m *Manager) StartPlanPaymentReturn(ctx context.Context, lang i18n.Lang, userID, planID int64, provider, returnURL string, periods int) (*model.PaymentOrder, error) {
	return m.startPlanPayment(ctx, lang, userID, planID, provider, returnURL, periods)
}

// lang is the language the PAYER is being served in, and it comes from the caller
// rather than from the user record: someone paying from an English subscription page
// may have no Telegram chat at all, and the invoice description they are about to
// read is on the provider's page, not in the bot.
func (m *Manager) startPlanPayment(ctx context.Context, lang i18n.Lang, userID, planID int64, provider, returnURL string, periods int) (*model.PaymentOrder, error) {
	return m.StartPurchase(ctx, lang, userID, PlanPurchase(planID, periods), provider, returnURL)
}

// OrderSubject names what an order bought, in lang: the plan, a balance top-up, a
// change or an add-on — for the operator's alerts and the payment instructions.
func OrderSubject(lang i18n.Lang, o *model.PaymentOrder) string { return orderSubject(lang, o) }

func orderSubject(lang i18n.Lang, o *model.PaymentOrder) string {
	if o.Kind == model.OrderTopup {
		return i18n.T(lang, "order.topupSubject")
	}
	return purchaseSubject(lang, o.Kind, o.PlanName, o.Periods, o.Devices, int(o.PackBytes>>30))
}

// planSubject names a plan purchase: "“Standard” plan", or "“Standard” plan × 3" for
// several periods.
func planSubject(lang i18n.Lang, plan string, periods int) string {
	if periods > 1 {
		return i18n.T(lang, "order.planSubjectN", plan, periods)
	}
	return i18n.T(lang, "order.planSubject", plan)
}

// startProviderOrder creates an order and the provider payment that collects it.
// describe renders the invoice description once the order has its number.
func (m *Manager) startProviderOrder(ctx context.Context, lang i18n.Lang, d store.OrderDraft, provider, returnURL string, describe func(orderID int64) string) (*model.PaymentOrder, error) {
	methods := m.PaymentMethods()
	if len(methods) == 0 {
		return nil, invalidCode("err.autoPayNotConfigured", "автоматическая оплата не настроена")
	}
	if provider == "" && len(methods) == 1 {
		provider = methods[0]
	}
	if !slices.Contains(methods, provider) {
		return nil, invalidCode("err.payMethodUnavailable", "способ оплаты недоступен")
	}

	// Reuse a fresh pending order for the same purchase instead of minting a new one
	// on every tap — stops a spammed "Pay" button from flooding provider API calls
	// and admin pings. Only within the reuse window, so the hosted pay URL is still
	// live, and only while it asks for the same money: a balance or discount that
	// changed since makes it a different order.
	if existing, err := m.store.LatestPendingProviderOrderForPlan(d.UserID, d.PlanID, provider); err == nil &&
		existing != nil && existing.PayURL != "" &&
		existing.Kind == d.Kind && existing.AmountRub == d.AmountRub &&
		existing.BalanceKop == d.BalanceKop && existing.PromoID == d.PromoID &&
		max(existing.Periods, 1) == max(d.Periods, 1) && sameExtras(existing, d) &&
		time.Now().Unix()-existing.CreatedAt < int64(providerOrderReuseWindow.Seconds()) {
		return existing, nil
	}

	client, err := m.providerClient(provider)
	if err != nil {
		return nil, err
	}
	order, err := m.store.CreateOrder(d, time.Now().Unix())
	if err != nil {
		return nil, orderCreateErr(err)
	}
	// The alerts below are read by the operator, the invoice description by the
	// payer — two audiences, two languages.
	adminLang := m.botLang()
	// A separate timeout context for the outbound provider call — ctx carries the
	// actor for the audit row and must not be cancelled along with the HTTP request.
	callCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if strings.TrimSpace(returnURL) == "" {
		returnURL = "https://t.me/"
	}
	providerID, payURL, err := client.Create(callCtx, payments.CreateReq{
		AmountRub:   order.AmountRub,
		OrderID:     order.ID,
		Description: describe(order.ID),
		ReturnURL:   returnURL,
		WebhookURL:  m.PaymentWebhookURL(provider),
	})
	if err != nil {
		_ = m.store.SetPaymentOrderStatus(order.ID, "cancelled", 0)
		// The provider error can carry credentials/response internals (e.g. YooKassa
		// 401 with the shopId hint) — log it for the operator, but return a clean,
		// generic message to the end user.
		logErr("payment: create failed", "provider", provider, "order", order.ID, "err", err)
		m.notifyAdminEvent(model.AdminEventPayment, i18n.T(adminLang, "notify.payNotCreated",
			order.ID, escHTML(m.methodLabel(adminLang, provider))))
		return nil, invalidCode("err.paymentCreateFailed", "не удалось создать платёж — попробуйте другой способ или позже")
	}
	if err := m.store.SetPaymentOrderProvider(order.ID, provider, providerID, payURL); err != nil {
		return nil, err
	}
	order.Provider, order.ProviderID, order.PayURL = provider, providerID, payURL
	m.supersedePromoOrders(ctx, d.UserID, d.PromoID, order.ID)
	m.notifyAdminEvent(model.AdminEventPayment, i18n.T(adminLang, "notify.payStarted",
		order.ID, escHTML(order.UserName), escHTML(orderSubject(adminLang, order)), order.AmountRub,
		escHTML(m.methodLabel(adminLang, provider))))
	m.audit(ctx, d.UserID, model.EventPaymentCreated, orderAudit(order, provider))
	m.EmitWebhook(model.WebhookPaymentCreated, order)
	return order, nil
}

// orderAudit is the journal payload for an order event.
func orderAudit(o *model.PaymentOrder, provider string) map[string]any {
	d := map[string]any{
		"order_id": o.ID, "amount_rub": o.AmountRub, "provider": provider,
	}
	if o.Kind == model.OrderTopup {
		d["kind"] = model.OrderTopup
	} else {
		d["plan"] = o.PlanName
	}
	if o.BalanceKop > 0 {
		d["balance_kop"] = o.BalanceKop
	}
	if o.PromoCode != "" {
		d["promo"] = o.PromoCode
		d["discount_rub"] = o.DiscountRub
	}
	return d
}

// amountMatches reports whether the charge the provider recorded is the one this
// order was created for. Amounts are fixed server-side at creation, so a mismatch
// means a tampered/misrouted callback or a provider anomaly — never a normal
// payment. Fails OPEN when the provider reported no readable amount: a format
// change on their side must not block real payments, and the callback is already
// authenticated (CryptoBot HMAC / YooKassa re-fetch over the API).
func amountMatches(order *model.PaymentOrder, paid payments.Result) bool {
	if paid.AmountKopecks <= 0 || paid.Currency == "" {
		return true // amount unknown → nothing to contradict
	}
	return paid.Currency == "RUB" && paid.AmountKopecks == int64(order.AmountRub)*100
}

// confirmProviderOrder applies the plan and marks the order paid. Idempotent: a
// re-delivered webhook or an overlapping poll is a no-op once the order is paid.
// paid carries the provider's view of the charge; it is verified against the order
// before any plan is granted.
func (m *Manager) confirmProviderOrder(provider, providerID string, paid payments.Result) error {
	_, _, err := m.confirmProviderOrderOutcome(provider, providerID, paid)
	return err
}

// confirmProviderOrderOutcome is confirmProviderOrder that also says what happened
// (a model.WebhookOutcome*) and which order it was, for the callback journal.
func (m *Manager) confirmProviderOrderOutcome(provider, providerID string, paid payments.Result) (string, int64, error) {
	order, err := m.store.GetPaymentOrderByProvider(provider, providerID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.WebhookOutcomeNoOrder, 0, err
	}
	if err != nil {
		return model.WebhookOutcomeError, 0, err
	}
	if order.Status == "paid" {
		return model.WebhookOutcomeDuplicate, order.ID, nil // a re-delivered webhook or an overlapping poll
	}
	// A cancelled order goes on like a pending one: the money was captured (YooKassa
	// has no invoice TTL and the 24h sweep fired; a newer checkout with the same promo
	// code superseded it; it was cancelled by hand), and it still buys what the order
	// was for — or lands on the balance when that can no longer be given.
	//
	// The charge must be the one this order was created for. Refuse to grant a plan
	// on a mismatch — the money situation then needs a human, so alert the operator
	// rather than silently applying (or silently dropping) it.
	if !amountMatches(order, paid) {
		logErr("payment: amount mismatch — plan not granted",
			"order", order.ID, "provider", provider,
			"expected_rub", order.AmountRub,
			"got", fmt.Sprintf("%d.%02d %s", paid.AmountKopecks/100, paid.AmountKopecks%100, paid.Currency))
		// Once per window, not once per poll. The order stays pending by design (a human
		// has to settle it), and the 25s provider poll keeps finding it paid-at-provider
		// — unthrottled that is thousands of identical alerts a day for a single order,
		// and it is a NORMAL outcome for an overpayment (PayPalych OVERPAID sends a
		// legitimately larger sum).
		if m.payNotice.should(strconv.FormatInt(order.ID, 10), time.Now()) {
			m.notifyAdminEvent(model.AdminEventPayment, i18n.T(m.botLang(), "notify.payMismatch",
				order.ID, escHTML(order.UserName), order.AmountRub,
				paid.AmountKopecks/100, paid.AmountKopecks%100, escHTML(paid.Currency)))
		}
		return model.WebhookOutcomeMismatch, order.ID, fmt.Errorf("payment amount does not match order %d", order.ID)
	}
	// Claim the pending→paid transition and grant the plan in one transaction. A
	// provider webhook and the polling fallback (or a re-delivered webhook) can reach
	// here for the same order concurrently; only the caller that wins the claim
	// applies the plan, so one payment can't extend the user twice. And because the
	// claim commits with the plan rather than before it, a crash mid-way cannot leave
	// the order paid with nothing delivered — see confirmOrderPaid.
	res, err := m.confirmOrderPaid(order, time.Now().Unix())
	if err != nil {
		return model.WebhookOutcomeError, order.ID, err
	}
	if !res.Claimed {
		return model.WebhookOutcomeDuplicate, order.ID, nil // another confirmer already handled this order
	}
	// The provider (or the polling fallback) confirmed this, not a person — so the
	// payment lands in the audit log as a system action.
	logInfo("payment: order paid", "order", order.ID, "provider", provider, "user", order.UserID, "plan", order.PlanID)
	m.afterOrderPaid(context.Background(), order, provider, res)
	return model.WebhookOutcomePaid, order.ID, nil
}

// afterOrderPaid tells everyone what a confirmed order did: the payer, the operator,
// the referrer, the journal and the webhooks.
func (m *Manager) afterOrderPaid(ctx context.Context, order *model.PaymentOrder, provider string, res store.ConfirmResult) {
	// An operator's own confirmation needs no alert back to the operators — only
	// when something did not go as the order said.
	byHand := provider == "manual"
	// A plan order whose plan could not be delivered: the store turned it into a
	// top-up, the money is on the balance.
	undelivered := (order.Kind == model.OrderPlan || isAddonKind(order.Kind)) && !res.PlanApplied
	set, _ := m.store.GetSettings()
	if u, e := m.store.GetUser(order.UserID); e == nil && set != nil {
		// Gated like the other user-facing notices, so an operator who turns them all
		// off does not still have the bot writing to people.
		lang := m.userLang(u.TgChatID)
		var msg string
		switch {
		case undelivered:
			msg = i18n.T(lang, "notify.userPaidToBalance", order.AmountRub, escHTML(order.PlanName))
		case order.Kind == model.OrderTopup:
			wal, _ := m.store.GetWalletLite(u.ID)
			msg = i18n.T(lang, "notify.userToppedUp", order.AmountRub, kopText(wal.BalanceKop))
		case isAddonKind(order.Kind):
			msg = i18n.T(lang, "notify.userPaidAddon", escHTML(orderSubject(lang, order)))
		default:
			msg = i18n.T(lang, "notify.userPaid", escHTML(m.PlanName(order.PlanID)))
		}
		m.notifyUserEvent(set, *u, model.UserNotifyPayment, msg)
	}
	adminLang := m.botLang()
	if !byHand {
		m.notifyAdminEvent(model.AdminEventPayment, i18n.T(adminLang, "notify.paid",
			order.ID, escHTML(order.UserName), escHTML(orderSubject(adminLang, order)), order.AmountRub,
			escHTML(m.methodLabel(adminLang, provider))))
	}
	if undelivered {
		m.notifyAdminEvent(model.AdminEventPayment, i18n.T(adminLang, "notify.payToBalance",
			order.ID, escHTML(order.UserName), escHTML(order.PlanName)))
		// The journal and the webhook report what the order is now, not a plan bought.
		order.Kind = model.OrderTopup
	}
	m.notifyReferral(set, res)
	order.Status = "paid"
	m.audit(ctx, order.UserID, model.EventPaymentPaid, orderAudit(order, provider))
	m.EmitWebhook(model.WebhookPaymentPaid, order)
}

// paymentOrderMaxAge bounds how long a pending provider order is polled before
// it's auto-cancelled as abandoned (the user opened a payment but never completed
// it), so the fallback poll doesn't hit the provider API forever for dead orders.
const paymentOrderMaxAge = 24 * time.Hour

// providerOrderReuseWindow is how long a just-created provider order is reused for
// the same plan+provider instead of creating another (anti-spam). Kept short so the
// reused hosted pay URL hasn't expired.
const providerOrderReuseWindow = 5 * time.Minute

// PollPendingPayments is the fallback for missed webhooks: it queries each pending
// provider order's status and confirms/cancels accordingly. Providers with no
// status endpoint (ErrNoStatusAPI) are left to their webhook — for those, a missed
// callback is only ever resolved by the abandoned sweep or by hand.
func (m *Manager) PollPendingPayments() {
	orders, err := m.store.PendingProviderOrders(100)
	if err != nil || len(orders) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	staleBefore := time.Now().Add(-paymentOrderMaxAge).Unix()
	// One client per provider for the whole sweep, so N pending orders on the same
	// provider don't rebuild (and re-read the config for) it N times.
	clients := map[string]payments.Client{}
	for _, o := range orders {
		// Abandoned orders (user never paid) would otherwise be polled forever — one
		// live provider API call each, every cycle. Age alone does NOT settle that
		// though: after any outage longer than the max age, every order waiting on us
		// looks abandoned, including the ones that were paid while we were down. So
		// age only ever decides what to do with an order the provider has already been
		// asked about — cancelling first and asking later is how money goes missing.
		stale := o.CreatedAt > 0 && o.CreatedAt < staleBefore
		client, ok := clients[o.Provider]
		if !ok {
			// A provider that's since been switched off or unconfigured has no client —
			// remember that (nil) so we don't retry building it for every order.
			client, _ = m.providerClient(o.Provider)
			clients[o.Provider] = client
		}
		if client == nil {
			// Nothing can confirm this order any more, so age is all there is to go on.
			if stale {
				m.cancelPendingOrder(o, "abandoned")
			}
			continue
		}
		res, err := client.Status(ctx, o.ProviderID)
		if err != nil {
			if errors.Is(err, payments.ErrNoStatusAPI) {
				// Webhook-only provider: there is no way to ask, so the age sweep is the
				// only thing that ever stops this order being polled.
				if stale {
					m.cancelPendingOrder(o, "abandoned")
				}
			} else {
				logErr("payment poll failed", "order", o.ID, "provider", o.Provider, "err", err)
				// A provider that keeps erroring on this invoice (404 for one it expired
				// on its side, a suspended merchant account) would otherwise leave the
				// order pending forever, polled every cycle. PendingProviderOrders reads a
				// fixed-size batch, so enough undead orders crowd out the real ones and
				// genuine payments stop being reconciled at all.
				if stale {
					m.cancelPendingOrder(o, "abandoned")
				}
			}
			continue
		}
		switch res.Status {
		case payments.StatusPaid:
			if err := m.confirmProviderOrder(o.Provider, o.ProviderID, res); err != nil {
				logErr("payment poll: confirm failed", "order", o.ID, "err", err)
			}
		case payments.StatusCanceled, payments.StatusRefunded:
			m.cancelPendingOrder(o, "provider_cancelled")
		default:
			// Still unpaid at the provider, and too old to keep asking about.
			if stale {
				m.cancelPendingOrder(o, "abandoned")
			}
		}
	}
}

// cancelPendingOrder cancels a still-pending order on the panel's own initiative
// (the 24h abandoned sweep, or the provider reporting it cancelled) and records it.
// A no-op — and no audit row — when someone else already resolved the order.
func (m *Manager) cancelPendingOrder(o model.PaymentOrder, reason string) {
	cancelled, err := m.store.CancelPaymentOrderIfPending(o.ID)
	if err != nil {
		return
	}
	if !cancelled {
		// The order was already delivered. Providers map a REFUND and a CHARGEBACK onto
		// the same "cancelled" status, so this is money being clawed back AFTER the plan
		// was granted — silently dropping it left the user subscribed on a payment that
		// no longer exists, with nothing in the journal to explain the shortfall.
		if o.Status == "paid" {
			m.audit(context.Background(), o.UserID, model.EventPaymentCancelled, map[string]any{
				"order_id": o.ID, "plan": o.PlanName, "amount_rub": o.AmountRub,
				"reason": reason, "after_delivery": true,
			})
			m.notifyAdminEvent(model.AdminEventPayment, fmt.Sprintf(
				i18n.T(m.botLang(), "notify.payRefundedAfterDelivery"),
				o.ID, escHTML(orderSubject(m.botLang(), &o)), o.AmountRub, escHTML(reason)))
		}
		return
	}
	m.audit(context.Background(), o.UserID, model.EventPaymentCancelled, map[string]any{
		"order_id": o.ID, "plan": o.PlanName, "amount_rub": o.AmountRub, "reason": reason,
	})
}

// HandleProviderWebhook processes a callback from provider key. The provider's
// client authenticates it (signature, or a re-fetch over the API for providers that
// sign nothing) and reports what it says about the payment; nothing here trusts the
// POST body. A callback for a provider that is off or unconfigured is refused —
// otherwise a stale/forged callback could still move an order. Every callback lands
// in the journal with what came of it.
func (m *Manager) HandleProviderWebhook(key string, body []byte, h http.Header, remoteIP string) error {
	rec := model.PaymentWebhook{
		At: time.Now().Unix(), Provider: key, RemoteIP: remoteIP,
		Headers: webhookHeaders(h), Body: string(body),
	}
	err := m.handleProviderWebhook(key, body, h, &rec)
	if err != nil {
		rec.Error = err.Error()
		if rec.Outcome == "" {
			rec.Outcome = model.WebhookOutcomeError
		}
	}
	if e := m.store.RecordPaymentWebhook(rec); e != nil {
		logErr("payment webhook: journal write failed", "provider", key, "err", e)
	}
	return err
}

func (m *Manager) handleProviderWebhook(key string, body []byte, h http.Header, rec *model.PaymentWebhook) error {
	client, err := m.providerClient(key)
	if err != nil {
		rec.Outcome = model.WebhookOutcomeRejected
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	providerID, res, err := client.Webhook(ctx, body, h)
	rec.ProviderID, rec.Status = providerID, string(res.Status)
	if err != nil {
		rec.Outcome = model.WebhookOutcomeRejected
		return err
	}
	if providerID == "" {
		rec.Outcome = model.WebhookOutcomeRejected
		return invalidCode("err.webhookNoPaymentID", "{{provider}}: в уведомлении нет идентификатора платежа", map[string]any{"provider": payments.Label(key)})
	}
	switch res.Status {
	case payments.StatusPaid:
		rec.Outcome, rec.OrderID, err = m.confirmProviderOrderOutcome(key, providerID, res)
		return err
	case payments.StatusCanceled:
		rec.Outcome = model.WebhookOutcomeCancelled
		o, e := m.store.GetPaymentOrderByProvider(key, providerID)
		if e != nil {
			rec.Outcome = model.WebhookOutcomeNoOrder
			return nil
		}
		rec.OrderID = o.ID
		m.cancelPendingOrder(*o, "provider_cancelled") // won't clobber an already-paid order
	case payments.StatusRefunded:
		rec.Outcome = model.WebhookOutcomeRefunded
		o, e := m.store.GetPaymentOrderByProvider(key, providerID)
		if e != nil {
			rec.Outcome = model.WebhookOutcomeNoOrder
			return nil
		}
		rec.OrderID = o.ID
		if o.Status == "paid" {
			m.providerRefunded(context.Background(), o)
		} else {
			m.cancelPendingOrder(*o, "provider_cancelled")
		}
	default:
		rec.Outcome = model.WebhookOutcomePending
		if o, e := m.store.GetPaymentOrderByProvider(key, providerID); e == nil {
			rec.OrderID = o.ID
		}
	}
	return nil
}

// webhookHeaders keeps the headers that explain a callback — its type and the
// provider's signature — as "Name: value" lines. Cookies, authorization and proxy
// hop headers stay out of the journal.
func webhookHeaders(h http.Header) string {
	names := make([]string, 0, len(h))
	for k := range h {
		switch strings.ToLower(k) {
		case "cookie", "authorization", "proxy-authorization", "x-forwarded-for", "x-real-ip", "forwarded":
			continue
		}
		names = append(names, k)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, k := range names {
		for _, v := range h[k] {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// PaymentWebhooks lists the callback journal.
func (m *Manager) PaymentWebhooks(f store.PaymentWebhookFilter) ([]model.PaymentWebhook, error) {
	return m.store.ListPaymentWebhooks(f)
}

// PurgeOldPaymentWebhooks drops callbacks past their retention window.
func (m *Manager) PurgeOldPaymentWebhooks() {
	cutoff := time.Now().AddDate(0, 0, -model.PaymentWebhookRetentionDays).Unix()
	if n, err := m.store.PurgePaymentWebhooks(cutoff); err != nil {
		logErr("payment webhooks: retention sweep failed", "err", err)
	} else if n > 0 {
		logInfo("payment webhooks: old rows purged", "count", n)
	}
}

// providerRefunded settles a paid order whose money the payment system returned —
// a refund made in its dashboard, or a chargeback: the order leaves revenue and can
// no longer be refunded to the balance (the user would get the money twice), what
// it put on a balance comes off, and a plan it bought loses the time it paid for.
// A repeated notification changes nothing.
func (m *Manager) providerRefunded(ctx context.Context, o *model.PaymentOrder) {
	r, err := m.store.ProviderRefundOrder(o.ID, time.Now().Unix())
	if errors.Is(err, store.ErrNotRefundable) {
		return
	}
	if err != nil {
		logErr("payment: provider refund not recorded", "order", o.ID, "err", err)
		return
	}
	cut := false
	// Devices or traffic the money bought go with it, while the user still holds them.
	if (r.Kind == model.OrderDevices || r.Kind == model.OrderTraffic) && r.Earlier == "" {
		if err := m.store.TakeBackAddon(o.UserID, o.PlanID, o.Devices*boolInt(r.Kind == model.OrderDevices), o.PackBytes); err != nil {
			logErr("payment: provider refund did not take the add-on back", "order", o.ID, "err", err)
		} else {
			cut = true
			m.TriggerUserSync()
		}
	}
	if r.Kind == model.OrderPlan && r.Earlier == "" {
		if u, err := m.store.GetUser(r.UserID); err == nil {
			if active := m.ActivePaidPlan(*u); active != nil && active.ID == o.PlanID {
				if err := m.takeBackTerm(ctx, *u, o); err != nil {
					logErr("payment: provider refund did not take the plan back", "order", o.ID, "err", err)
				} else {
					cut = true
				}
			}
		}
	}
	m.audit(ctx, r.UserID, model.EventPaymentRefunded, map[string]any{
		"order_id": o.ID, "plan": o.PlanName, "amount_rub": o.AmountRub, "source": model.RefundByProvider,
		"taken_kop": r.TakenKop, "returned_kop": r.ReturnedKop, "ref_taken_kop": r.RefTakenKop,
		"short_kop": r.ShortKop, "ref_short_kop": r.RefShortKop, "ref_days": r.RefDays,
		"plan_cut": cut,
	})
	lang := m.botLang()
	var done []string
	if r.Earlier == model.RefundToBalance {
		done = append(done, i18n.T(lang, "notify.refundWasBalance"))
	}
	if cut {
		done = append(done, i18n.T(lang, "notify.refundPlanCut"))
	}
	if r.TakenKop > 0 {
		done = append(done, i18n.T(lang, "notify.refundTaken", kopText(r.TakenKop)))
	}
	if r.ReturnedKop > 0 {
		done = append(done, i18n.T(lang, "notify.refundReturned", kopText(r.ReturnedKop)))
	}
	if r.RefTakenKop > 0 {
		done = append(done, i18n.T(lang, "notify.refundRefTaken", kopText(r.RefTakenKop)))
	}
	if r.RefDays > 0 {
		done = append(done, i18n.T(lang, "notify.refundRefDays", r.RefDays))
	}
	if r.ShortKop > 0 {
		done = append(done, i18n.T(lang, "notify.refundShort", kopText(r.ShortKop)))
	}
	if r.RefShortKop > 0 {
		done = append(done, i18n.T(lang, "notify.refundRefShort", kopText(r.RefShortKop)))
	}
	if after, err := m.store.GetPaymentOrder(o.ID); err == nil {
		m.EmitWebhook(model.WebhookPaymentRefunded, after)
	}
	msg := i18n.T(lang, "notify.providerRefund", o.ID, escHTML(o.UserName),
		escHTML(orderSubject(lang, o)), o.AmountRub, escHTML(payments.Label(o.Provider)))
	if len(done) > 0 {
		msg += "\n" + strings.Join(done, "\n")
	}
	m.notifyAdminEvent(model.AdminEventPayment, msg)
}

// sameExtras reports whether a pending order buys the same add-ons as a draft.
func sameExtras(o *model.PaymentOrder, d store.OrderDraft) bool {
	return o.Devices == d.Devices && o.ChangeFrom == d.ChangeFrom &&
		o.ExpectExpire == d.ExpectExpire && o.PackBytes == d.PackBytes
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
