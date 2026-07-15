package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// RioPay is a RUB card/SBP gateway with the simplest contract of the P2P set:
// a single API token in the X-Api-Token header (no per-request signing), and a
// webhook signed as HMAC-SHA512 over the raw body in the X-Signature header.

const keyRioPay = "riopay"

const rioPayAPI = "https://api.riopay.online/v1"

func rioPayDescriptor() Descriptor {
	return Descriptor{
		Key:   keyRioPay,
		Label: "RioPay",
		Note:  "Карты, СБП · ₽",
		Fields: []Field{
			{Key: "api_token", Label: "API-токен", Kind: FieldSecret},
			{Key: "webhook_secret", Label: "Секрет вебхука", Kind: FieldSecret, Optional: true,
				Help: "Необязательно. Если пусто — вебхук подписывается API-токеном."},
		},
		New: func(cfg Config) Client {
			return &RioPay{token: cfg.Get("api_token"), webhookSecret: cfg.Get("webhook_secret")}
		},
	}
}

// RioPay is a minimal RioPay client.
type RioPay struct {
	token         string
	webhookSecret string
	base          string // overridable in tests
}

func (r *RioPay) endpoint() string {
	if r.base != "" {
		return r.base
	}
	return rioPayAPI
}

func (r *RioPay) headers() map[string]string {
	return map[string]string{"X-Api-Token": r.token}
}

// Create opens an order. RioPay wants the amount as a decimal string in rubles and
// echoes our order id back in the webhook as externalId.
func (r *RioPay) Create(ctx context.Context, req CreateReq) (string, string, error) {
	body := map[string]any{
		"amount":     rubles(req.AmountRub),
		"externalId": fmt.Sprintf("%d", req.OrderID),
		"purpose":    req.Description,
	}
	if req.ReturnURL != "" {
		body["successUrl"] = req.ReturnURL
	}
	var out struct {
		ID          string `json:"id"`
		PaymentLink string `json:"paymentLink"`
		Message     string `json:"message"`
		Error       string `json:"error"`
	}
	if err := callJSON(ctx, "RioPay", http.MethodPost, r.endpoint()+"/orders", r.headers(), body, &out); err != nil {
		return "", "", err
	}
	if out.ID == "" || out.PaymentLink == "" {
		return "", "", fmt.Errorf("RioPay: пустой ответ при создании заказа: %s%s", out.Message, out.Error)
	}
	return out.ID, out.PaymentLink, nil
}

// Status re-reads an order by RioPay's order uuid.
func (r *RioPay) Status(ctx context.Context, providerID string) (Result, error) {
	var out rioPayOrder
	if err := callJSON(ctx, "RioPay", http.MethodGet, r.endpoint()+"/orders/"+providerID, r.headers(), nil, &out); err != nil {
		return Result{}, err
	}
	return out.result(), nil
}

// Webhook authenticates the raw body against the X-Signature header (HMAC-SHA512).
func (r *RioPay) Webhook(_ context.Context, body []byte, h http.Header) (string, Result, error) {
	sig := h.Get("X-Signature")
	if sig == "" {
		return "", Result{}, fmt.Errorf("RioPay: нет подписи")
	}
	key := r.webhookSecret
	if key == "" {
		key = r.token
	}
	if !eqSig(hmacSHA512Hex(key, string(body)), sig) {
		return "", Result{}, fmt.Errorf("RioPay: неверная подпись")
	}
	var out rioPayOrder
	if json.Unmarshal(body, &out) != nil || out.ID == "" {
		return "", Result{}, fmt.Errorf("RioPay: некорректное уведомление")
	}
	return out.ID, out.result(), nil
}

type rioPayOrder struct {
	ID       string  `json:"id"`
	Status   string  `json:"status"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

func (o rioPayOrder) result() Result {
	currency := o.Currency
	if currency == "" {
		currency = "RUB" // RioPay omits currency; the account is RUB
	}
	amount := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", o.Amount), "0"), ".")
	switch strings.ToUpper(o.Status) {
	case "COMPLETED":
		return amountResult(StatusPaid, amount, currency)
	case "CANCELED", "FAILED", "EXPIRED":
		return Result{Status: StatusCanceled}
	default: // CREATED, PENDING
		return Result{Status: StatusPending}
	}
}
