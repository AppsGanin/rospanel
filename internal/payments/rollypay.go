package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// RollyPay takes RUB card/SBP payments and settles the merchant in USDT — the
// conversion happens on their side, so the panel always sends and reconciles
// rubles. Auth is an API key in X-API-Key plus a per-request X-Nonce. The webhook
// is HMAC-SHA256 over "{X-Timestamp}.{raw body}" in the X-Signature header.

const keyRollyPay = "rollypay"

const rollyPayAPI = "https://rollypay.io/api/v1"

func rollyPayDescriptor() Descriptor {
	return Descriptor{
		Key:   keyRollyPay,
		Label: "RollyPay",
		Note:  "Карты, СБП · ₽",
		Fields: []Field{
			{Key: "api_key", Label: "API-ключ", Kind: FieldSecret, Placeholder: "rpk_live_…"},
			{Key: "signing_secret", Label: "Signing secret", Kind: FieldSecret,
				Help: "Ключ для проверки подписи вебхука."},
		},
		New: func(cfg Config) Client {
			return &RollyPay{apiKey: cfg.Get("api_key"), signingSecret: cfg.Get("signing_secret")}
		},
	}
}

// RollyPay is a minimal RollyPay client.
type RollyPay struct {
	apiKey        string
	signingSecret string
	base          string // overridable in tests
}

func (r *RollyPay) endpoint() string {
	if r.base != "" {
		return r.base
	}
	return rollyPayAPI
}

func (r *RollyPay) headers() map[string]string {
	return map[string]string{"X-API-Key": r.apiKey, "X-Nonce": uuid.NewString()}
}

// Create opens a payment and returns its id plus the hosted pay URL.
func (r *RollyPay) Create(ctx context.Context, req CreateReq) (string, string, error) {
	body := map[string]any{
		"amount":           rubles(req.AmountRub),
		"payment_currency": "RUB",
		"order_id":         fmt.Sprintf("%d", req.OrderID),
		"description":      req.Description,
	}
	if req.ReturnURL != "" {
		body["redirect_url"] = req.ReturnURL
	}
	var out struct {
		PaymentID string `json:"payment_id"`
		PayURL    string `json:"pay_url"`
		Message   string `json:"message"`
		Error     string `json:"error"`
	}
	if err := callJSON(ctx, "RollyPay", http.MethodPost, r.endpoint()+"/payments", r.headers(), body, &out); err != nil {
		return "", "", err
	}
	if out.PaymentID == "" || out.PayURL == "" {
		return "", "", fmt.Errorf("RollyPay: пустой ответ при создании платежа: %s%s", out.Message, out.Error)
	}
	return out.PaymentID, out.PayURL, nil
}

// Status re-reads a payment by its id.
func (r *RollyPay) Status(ctx context.Context, providerID string) (Result, error) {
	var out rollyPayPayment
	if err := callJSON(ctx, "RollyPay", http.MethodGet, r.endpoint()+"/payments/"+providerID, r.headers(), nil, &out); err != nil {
		return Result{}, err
	}
	return out.result(), nil
}

// Webhook authenticates the raw body: HMAC-SHA256 of "{timestamp}.{body}" against
// the X-Signature header. The timestamp is part of the signed message, so a body or
// timestamp tampered in transit fails the check.
func (r *RollyPay) Webhook(_ context.Context, body []byte, h http.Header) (string, Result, error) {
	sig, ts := h.Get("X-Signature"), h.Get("X-Timestamp")
	if sig == "" || ts == "" {
		return "", Result{}, fmt.Errorf("RollyPay: нет подписи")
	}
	if !eqSig(hmacSHA256Hex(r.signingSecret, ts+"."+string(body)), sig) {
		return "", Result{}, fmt.Errorf("RollyPay: неверная подпись")
	}
	var out rollyPayPayment
	if json.Unmarshal(body, &out) != nil || out.PaymentID == "" {
		return "", Result{}, fmt.Errorf("RollyPay: некорректное уведомление")
	}
	res := out.result()
	// A "payment.paid" event is itself the paid signal even if the status field lags.
	// Re-derive the amount from the body (not res, which drops it on the non-"paid"
	// branch) so the amount is still cross-checked against the order.
	if out.EventType == "payment.paid" {
		res = amountResult(StatusPaid, out.Amount, rollyCurrency(out.Currency))
	}
	return out.PaymentID, res, nil
}

type rollyPayPayment struct {
	PaymentID string `json:"payment_id"`
	EventType string `json:"event_type"`
	Status    string `json:"status"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
}

// rollyCurrency defaults an empty currency to RUB (the merchant account currency).
func rollyCurrency(c string) string {
	if c == "" {
		return "RUB"
	}
	return c
}

func (p rollyPayPayment) result() Result {
	switch p.Status {
	case "paid":
		return amountResult(StatusPaid, p.Amount, rollyCurrency(p.Currency))
	case "expired", "canceled", "chargeback":
		return Result{Status: StatusCanceled}
	default: // created, processing
		return Result{Status: StatusPending}
	}
}
