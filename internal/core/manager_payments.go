package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/payments"
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
		if ok && p.Enabled && d.Configured(p.Config) {
			out = append(out, d.Key)
		}
	}
	return out
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
		return invalid("неизвестный способ оплаты")
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
			return invalid("%s: заполните «%s»", d.Label, f.Label)
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
		return nil, invalid("неизвестный способ оплаты")
	}
	p, err := m.store.GetPaymentProvider(key)
	if err != nil {
		return nil, err
	}
	if !p.Enabled {
		return nil, invalid("%s: способ оплаты выключен", d.Label)
	}
	if !d.Configured(p.Config) {
		return nil, invalid("%s: не заполнены настройки", d.Label)
	}
	return d.New(p.Config), nil
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
	set, _ := m.Settings()
	if set == nil || set.PaymentWebhookSecret == "" || set.Host == "" {
		return ""
	}
	return "https://" + set.Host + "/" + set.PaymentWebhookSecret + "/" + key
}

// StartPlanPayment creates an order plus a provider payment and returns the order
// with its hosted pay URL. provider may be "" when exactly one method is enabled.
// The payer is returned to Telegram after paying (the bot flow).
func (m *Manager) StartPlanPayment(ctx context.Context, userID, planID int64, provider string) (*model.PaymentOrder, error) {
	return m.startPlanPayment(ctx, userID, planID, provider, "https://t.me/")
}

// StartPlanPaymentReturn is StartPlanPayment for the web subscription page: it
// sends the payer back to returnURL (the sub page) after a card payment instead of
// to Telegram. returnURL is used by hosted-form providers (YooKassa); CryptoBot
// ignores it.
func (m *Manager) StartPlanPaymentReturn(ctx context.Context, userID, planID int64, provider, returnURL string) (*model.PaymentOrder, error) {
	return m.startPlanPayment(ctx, userID, planID, provider, returnURL)
}

func (m *Manager) startPlanPayment(ctx context.Context, userID, planID int64, provider, returnURL string) (*model.PaymentOrder, error) {
	plan, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return nil, invalid("тариф не найден")
	}
	if plan.IsFree() {
		return nil, invalid("этот тариф бесплатный")
	}
	// No switching between plans while a paid one is active: the user must cancel
	// the current subscription first. Paying for the SAME plan (renewal/extension)
	// is always allowed. A trial or free plan never blocks buying a paid one. Fail
	// closed on a user-read error so the guard can't be bypassed.
	u, err := m.store.GetUser(userID)
	if err != nil {
		return nil, err
	}
	if u.PlanID != planID {
		// A disabled plan can't be bought as a new purchase/switch, but an existing
		// subscriber may still renew the plan they're already on (grandfathering).
		if !plan.Enabled {
			return nil, invalid("тариф недоступен")
		}
		if cur := m.ActivePaidPlan(*u); cur != nil {
			return nil, invalid("у вас активна подписка «%s» — сначала отмените её, чтобы сменить тариф", cur.Name)
		}
	}
	methods := m.PaymentMethods()
	if len(methods) == 0 {
		return nil, invalid("автоматическая оплата не настроена")
	}
	if provider == "" && len(methods) == 1 {
		provider = methods[0]
	}
	if !slices.Contains(methods, provider) {
		return nil, invalid("способ оплаты недоступен")
	}

	// Reuse a fresh pending order for the same plan+provider instead of minting a new
	// one on every tap — stops a spammed "Pay" button from flooding provider API calls
	// and admin pings. Only within the reuse window, so the hosted pay URL is still live.
	if existing, err := m.store.LatestPendingProviderOrderForPlan(userID, planID, provider); err == nil &&
		existing != nil && existing.PayURL != "" &&
		time.Now().Unix()-existing.CreatedAt < int64(providerOrderReuseWindow.Seconds()) {
		return existing, nil
	}

	client, err := m.providerClient(provider)
	if err != nil {
		return nil, err
	}
	order, err := m.store.CreatePaymentOrder(userID, planID, plan.PriceRub)
	if err != nil {
		return nil, err
	}
	// A separate timeout context for the outbound provider call — ctx carries the
	// actor for the audit row and must not be cancelled along with the HTTP request.
	callCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if strings.TrimSpace(returnURL) == "" {
		returnURL = "https://t.me/"
	}
	providerID, payURL, err := client.Create(callCtx, payments.CreateReq{
		AmountRub:   plan.PriceRub,
		OrderID:     order.ID,
		Description: fmt.Sprintf("Тариф «%s», заказ #%d", plan.Name, order.ID),
		ReturnURL:   returnURL,
		WebhookURL:  m.PaymentWebhookURL(provider),
	})
	if err != nil {
		_ = m.store.SetPaymentOrderStatus(order.ID, "cancelled", 0)
		// The provider error can carry credentials/response internals (e.g. YooKassa
		// 401 with the shopId hint) — log it for the operator, but return a clean,
		// generic message to the end user.
		logErr("payment: create failed", "provider", provider, "order", order.ID, "err", err)
		m.notifyAdminEvent(model.AdminEventPayment, fmt.Sprintf(
			"⚠️ <b>Платёж не создан</b>\nЗаказ #%d · способ %s\nПроверьте настройки провайдера.",
			order.ID, payments.Label(provider)))
		return nil, invalid("не удалось создать платёж — попробуйте другой способ или позже")
	}
	if err := m.store.SetPaymentOrderProvider(order.ID, provider, providerID, payURL); err != nil {
		return nil, err
	}
	order.Provider, order.ProviderID, order.PayURL = provider, providerID, payURL
	m.notifyAdminEvent(model.AdminEventPayment, fmt.Sprintf(
		"🛒 <b>Начата оплата</b>\nЗаказ #%d · %s\nТариф: %s · %d ₽\nСпособ: %s",
		order.ID, escHTML(order.UserName), escHTML(plan.Name), plan.PriceRub, payments.Label(provider)))
	m.audit(ctx, userID, model.EventPaymentCreated, map[string]any{
		"order_id": order.ID, "plan": plan.Name, "amount_rub": plan.PriceRub, "provider": provider,
	})
	m.EmitWebhook(model.WebhookPaymentCreated, order)
	return order, nil
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
	order, err := m.store.GetPaymentOrderByProvider(provider, providerID)
	if err != nil {
		return err
	}
	if order.Status != "pending" {
		// A payment that lands after the order was auto-cancelled (e.g. YooKassa has no
		// invoice TTL and the 24h sweep already fired): money was captured but no plan
		// applied — flag it so the operator can apply the tariff by hand.
		if order.Status == "cancelled" {
			m.notifyAdminEvent(model.AdminEventPayment, fmt.Sprintf(
				"⚠️ <b>Оплата по отменённому заказу</b>\nЗаказ #%d · %s\nТариф: %s · %d ₽\nОплата пришла после отмены — примените тариф вручную.",
				order.ID, escHTML(order.UserName), escHTML(order.PlanName), order.AmountRub))
		}
		return nil
	}
	// The charge must be the one this order was created for. Refuse to grant a plan
	// on a mismatch — the money situation then needs a human, so alert the operator
	// rather than silently applying (or silently dropping) it.
	if !amountMatches(order, paid) {
		logErr("payment: amount mismatch — plan not granted",
			"order", order.ID, "provider", provider,
			"expected_rub", order.AmountRub,
			"got", fmt.Sprintf("%d.%02d %s", paid.AmountKopecks/100, paid.AmountKopecks%100, paid.Currency))
		m.notifyAdminEvent(model.AdminEventPayment, fmt.Sprintf(
			"⚠️ <b>Сумма оплаты не совпала</b>\nЗаказ #%d · %s\nОжидалось: %d ₽ · пришло: %d.%02d %s\nТариф НЕ выдан — проверьте платёж вручную.",
			order.ID, escHTML(order.UserName), order.AmountRub,
			paid.AmountKopecks/100, paid.AmountKopecks%100, escHTML(paid.Currency)))
		return fmt.Errorf("сумма оплаты не совпадает с заказом %d", order.ID)
	}
	// Atomically claim the pending→paid transition. A provider webhook and the
	// polling fallback (or a re-delivered webhook) can reach here for the same order
	// concurrently; only the caller that wins the CAS applies the plan, so one
	// payment can't extend the user twice.
	claimed, err := m.store.MarkPaymentOrderPaidIfPending(order.ID, time.Now().Unix())
	if err != nil {
		return err
	}
	if !claimed {
		return nil // another confirmer already handled this order
	}
	// The provider (or the polling fallback) confirmed this, not a person — so the
	// plan change and the payment land in the audit log as system actions.
	ctx := context.Background()
	// Extend from the current expiry only for a renewal of the active paid plan;
	// buying from trial/free/expired starts from now (no inherited time). Audited as
	// the payment below, not as a bare plan switch — one purchase is one event.
	if err := m.applyPlan(ctx, order.UserID, order.PlanID, m.isPlanRenewal(order.UserID, order.PlanID), ""); err != nil {
		// Roll the claim back so the polling fallback retries rather than leaving a
		// paid order whose plan was never applied.
		_ = m.store.RevertPaymentOrderToPending(order.ID)
		return err
	}
	logInfo("payment: order paid", "order", order.ID, "provider", provider, "user", order.UserID, "plan", order.PlanID)
	if u, e := m.store.GetUser(order.UserID); e == nil {
		// Gated like the other user-facing notices, so an operator who turns them all
		// off does not still have the bot writing to people.
		if set, err := m.store.GetSettings(); err == nil {
			m.notifyUserEvent(set, *u, model.UserNotifyPayment, fmt.Sprintf(
				"✅ Оплата получена. Тариф «%s» активирован.", m.PlanName(order.PlanID)))
		}
	}
	m.notifyAdminEvent(model.AdminEventPayment, fmt.Sprintf(
		"✅ <b>Оплачено</b>\nЗаказ #%d · %s\nТариф: %s · %d ₽\nСпособ: %s",
		order.ID, escHTML(order.UserName), escHTML(order.PlanName), order.AmountRub, payments.Label(provider)))
	order.Status = "paid"
	m.audit(ctx, order.UserID, model.EventPaymentPaid, map[string]any{
		"order_id": order.ID, "plan": order.PlanName, "amount_rub": order.AmountRub, "provider": provider,
	})
	m.EmitWebhook(model.WebhookPaymentPaid, order)
	return nil
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
		// live provider API call each, every cycle. Cancel anything older than the
		// max age and stop polling it.
		if o.CreatedAt > 0 && o.CreatedAt < staleBefore {
			m.cancelPendingOrder(o, "abandoned")
			continue
		}
		client, ok := clients[o.Provider]
		if !ok {
			// A provider that's since been switched off or unconfigured has no client —
			// remember that (nil) so we don't retry building it for every order.
			client, _ = m.providerClient(o.Provider)
			clients[o.Provider] = client
		}
		if client == nil {
			continue
		}
		res, err := client.Status(ctx, o.ProviderID)
		if err != nil {
			if !errors.Is(err, payments.ErrNoStatusAPI) {
				logErr("payment poll failed", "order", o.ID, "provider", o.Provider, "err", err)
			}
			continue
		}
		switch res.Status {
		case payments.StatusPaid:
			if err := m.confirmProviderOrder(o.Provider, o.ProviderID, res); err != nil {
				logErr("payment poll: confirm failed", "order", o.ID, "err", err)
			}
		case payments.StatusCanceled:
			m.cancelPendingOrder(o, "provider_cancelled")
		}
	}
}

// cancelPendingOrder cancels a still-pending order on the panel's own initiative
// (the 24h abandoned sweep, or the provider reporting it cancelled) and records it.
// A no-op — and no audit row — when someone else already resolved the order.
func (m *Manager) cancelPendingOrder(o model.PaymentOrder, reason string) {
	cancelled, err := m.store.CancelPaymentOrderIfPending(o.ID)
	if err != nil || !cancelled {
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
// otherwise a stale/forged callback could still move an order.
func (m *Manager) HandleProviderWebhook(key string, body []byte, h http.Header) error {
	client, err := m.providerClient(key)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	providerID, res, err := client.Webhook(ctx, body, h)
	if err != nil {
		return err
	}
	if providerID == "" {
		return invalid("%s: в уведомлении нет идентификатора платежа", payments.Label(key))
	}
	switch res.Status {
	case payments.StatusPaid:
		return m.confirmProviderOrder(key, providerID, res)
	case payments.StatusCanceled:
		if o, e := m.store.GetPaymentOrderByProvider(key, providerID); e == nil {
			m.cancelPendingOrder(*o, "provider_cancelled") // won't clobber an already-paid order
		}
	}
	return nil
}
