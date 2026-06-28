package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
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

func (m *Manager) notifyAdmin(html string) {
	m.notifyMu.Lock()
	fn := m.adminNotify
	m.notifyMu.Unlock()
	if fn != nil {
		fn(html)
	}
}

// providerLabel is a human name for a payment provider key.
func providerLabel(p string) string {
	switch p {
	case payments.ProviderYooKassa:
		return "ЮКасса (карта)"
	case payments.ProviderCryptoBot:
		return "CryptoBot (крипта)"
	default:
		return "вручную"
	}
}

// PaymentMethods returns the enabled, fully-configured provider keys.
func (m *Manager) PaymentMethods() []string {
	set, err := m.Settings()
	if err != nil {
		return nil
	}
	var out []string
	if set.YooKassaEnabled && set.YooKassaShopID != "" && set.YooKassaSecretKey != "" {
		out = append(out, payments.ProviderYooKassa)
	}
	if set.CryptoBotEnabled && set.CryptoBotToken != "" {
		out = append(out, payments.ProviderCryptoBot)
	}
	return out
}

// SavePaymentSettings validates and persists provider config. An empty secret /
// token is treated as "keep current" so toggling doesn't wipe stored credentials.
// The webhook secret is generated on first save.
func (m *Manager) SavePaymentSettings(st *model.Settings) error {
	cur, err := m.Settings()
	if err != nil {
		return err
	}
	st.YooKassaShopID = strings.TrimSpace(st.YooKassaShopID)
	st.YooKassaSecretKey = strings.TrimSpace(st.YooKassaSecretKey)
	st.CryptoBotToken = strings.TrimSpace(st.CryptoBotToken)
	if st.YooKassaSecretKey == "" {
		st.YooKassaSecretKey = cur.YooKassaSecretKey
	}
	if st.CryptoBotToken == "" {
		st.CryptoBotToken = cur.CryptoBotToken
	}
	if st.YooKassaEnabled && (st.YooKassaShopID == "" || st.YooKassaSecretKey == "") {
		return invalid("укажите shopId и секретный ключ ЮКассы")
	}
	if st.CryptoBotEnabled && st.CryptoBotToken == "" {
		return invalid("укажите токен CryptoBot")
	}
	if err := m.store.SetPaymentSettings(st); err != nil {
		return err
	}
	return m.ensureWebhookSecret()
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

// PaymentWebhookURLs returns the public webhook URLs to paste into the provider
// dashboards. Empty when the host or secret isn't known yet.
func (m *Manager) PaymentWebhookURLs() (yookassa, cryptobot string) {
	set, _ := m.Settings()
	if set == nil || set.PaymentWebhookSecret == "" || set.Host == "" {
		return "", ""
	}
	base := "https://" + set.Host + "/" + set.PaymentWebhookSecret
	return base + "/yookassa", base + "/cryptobot"
}

// StartPlanPayment creates an order plus a provider payment and returns the order
// with its hosted pay URL. provider may be "" when exactly one method is enabled.
func (m *Manager) StartPlanPayment(userID, planID int64, provider string) (*model.PaymentOrder, error) {
	plan, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return nil, err
	}
	if plan.IsFree || plan.PriceRub <= 0 {
		return nil, invalid("этот тариф бесплатный")
	}
	set, err := m.Settings()
	if err != nil {
		return nil, err
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

	order, err := m.store.CreatePaymentOrder(userID, planID, plan.PriceRub)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	desc := fmt.Sprintf("Тариф «%s», заказ #%d", plan.Name, order.ID)

	var providerID, payURL string
	switch provider {
	case payments.ProviderYooKassa:
		yk := payments.NewYooKassa(set.YooKassaShopID, set.YooKassaSecretKey)
		providerID, payURL, err = yk.CreatePayment(ctx, plan.PriceRub, order.ID, desc, "https://t.me/")
	case payments.ProviderCryptoBot:
		cb := payments.NewCryptoBot(set.CryptoBotToken, set.CryptoBotTestnet)
		providerID, payURL, err = cb.CreateInvoice(ctx, plan.PriceRub, order.ID, desc)
	}
	if err != nil {
		_ = m.store.SetPaymentOrderStatus(order.ID, "cancelled", 0)
		return nil, invalid("не удалось создать платёж: %s", err.Error())
	}
	if err := m.store.SetPaymentOrderProvider(order.ID, provider, providerID, payURL); err != nil {
		return nil, err
	}
	order.Provider, order.ProviderID, order.PayURL = provider, providerID, payURL
	m.notifyAdmin(fmt.Sprintf(
		"🛒 <b>Начата оплата</b>\nЗаказ #%d · %s\nТариф: %s · %d ₽\nСпособ: %s",
		order.ID, escHTML(order.UserName), escHTML(plan.Name), plan.PriceRub, providerLabel(provider)))
	return order, nil
}

// confirmProviderOrder applies the plan and marks the order paid. Idempotent: a
// re-delivered webhook or an overlapping poll is a no-op once the order is paid.
func (m *Manager) confirmProviderOrder(provider, providerID string) error {
	order, err := m.store.GetPaymentOrderByProvider(provider, providerID)
	if err != nil {
		return err
	}
	if order.Status != "pending" {
		return nil
	}
	if err := m.ApplyPlanToUser(order.UserID, order.PlanID, true); err != nil {
		return err
	}
	if err := m.store.SetPaymentOrderStatus(order.ID, "paid", time.Now().Unix()); err != nil {
		return err
	}
	logInfo("payment: order %d paid via %s, user %d plan %d", order.ID, provider, order.UserID, order.PlanID)
	if u, e := m.store.GetUser(order.UserID); e == nil {
		m.notifyUser(u.TgChatID, fmt.Sprintf("✅ Оплата получена. Тариф «%s» активирован.", m.PlanName(order.PlanID)))
	}
	m.notifyAdmin(fmt.Sprintf(
		"✅ <b>Оплачено</b>\nЗаказ #%d · %s\nТариф: %s · %d ₽\nСпособ: %s",
		order.ID, escHTML(order.UserName), escHTML(order.PlanName), order.AmountRub, providerLabel(provider)))
	return nil
}

// PollPendingPayments is the fallback for missed webhooks: it queries each pending
// provider order's status and confirms/cancels accordingly.
func (m *Manager) PollPendingPayments() {
	orders, err := m.store.PendingProviderOrders(100)
	if err != nil || len(orders) == 0 {
		return
	}
	set, err := m.Settings()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, o := range orders {
		var status payments.Status
		var perr error
		switch o.Provider {
		case payments.ProviderYooKassa:
			if !set.YooKassaEnabled {
				continue
			}
			status, perr = payments.NewYooKassa(set.YooKassaShopID, set.YooKassaSecretKey).PaymentStatus(ctx, o.ProviderID)
		case payments.ProviderCryptoBot:
			if !set.CryptoBotEnabled {
				continue
			}
			status, perr = payments.NewCryptoBot(set.CryptoBotToken, set.CryptoBotTestnet).InvoiceStatus(ctx, o.ProviderID)
		default:
			continue
		}
		if perr != nil {
			logErr("payment poll: order %d: %v", o.ID, perr)
			continue
		}
		switch status {
		case payments.StatusPaid:
			if err := m.confirmProviderOrder(o.Provider, o.ProviderID); err != nil {
				logErr("payment poll: confirm order %d: %v", o.ID, err)
			}
		case payments.StatusCanceled:
			_ = m.store.SetPaymentOrderStatus(o.ID, "cancelled", 0)
		}
	}
}

// HandleYooKassaWebhook processes a notification. The POST body is not trusted —
// the payment is re-fetched via the API before the order is confirmed.
func (m *Manager) HandleYooKassaWebhook(body []byte) error {
	set, err := m.Settings()
	if err != nil || !set.YooKassaEnabled {
		return invalid("ЮКасса выключена")
	}
	var n struct {
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if json.Unmarshal(body, &n) != nil || n.Object.ID == "" {
		return invalid("некорректное уведомление")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, err := payments.NewYooKassa(set.YooKassaShopID, set.YooKassaSecretKey).PaymentStatus(ctx, n.Object.ID)
	if err != nil {
		return err
	}
	switch status {
	case payments.StatusPaid:
		return m.confirmProviderOrder(payments.ProviderYooKassa, n.Object.ID)
	case payments.StatusCanceled:
		if o, e := m.store.GetPaymentOrderByProvider(payments.ProviderYooKassa, n.Object.ID); e == nil {
			_ = m.store.SetPaymentOrderStatus(o.ID, "cancelled", 0)
		}
	}
	return nil
}

// HandleCryptoBotWebhook verifies the signature and confirms a paid invoice.
func (m *Manager) HandleCryptoBotWebhook(body []byte, signature string) error {
	set, err := m.Settings()
	if err != nil || !set.CryptoBotEnabled {
		return invalid("CryptoBot выключен")
	}
	cb := payments.NewCryptoBot(set.CryptoBotToken, set.CryptoBotTestnet)
	if !cb.VerifyWebhook(body, signature) {
		return invalid("неверная подпись")
	}
	var upd struct {
		UpdateType string `json:"update_type"`
		Payload    struct {
			InvoiceID int64  `json:"invoice_id"`
			Status    string `json:"status"`
		} `json:"payload"`
	}
	if json.Unmarshal(body, &upd) != nil {
		return invalid("некорректное уведомление")
	}
	if upd.UpdateType == "invoice_paid" || upd.Payload.Status == "paid" {
		return m.confirmProviderOrder(payments.ProviderCryptoBot, fmt.Sprintf("%d", upd.Payload.InvoiceID))
	}
	return nil
}
