package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// AuraPay (aurapay.tech) is a RUB card/SBP gateway. Auth is an API key + shop id in
// headers. Its webhook is HMAC-signed (concatenated field values ordered by key),
// which is fiddly to reproduce; since AuraPay has a status endpoint, the callback is
// confirmed the robust way instead — pull the invoice id and re-fetch it over the
// authenticated status API. That also means the webhook secret key isn't needed.

const keyAuraPay = "aurapay"

const auraPayAPI = "https://app.aurapay.tech"

func auraPayDescriptor() Descriptor {
	return Descriptor{
		Key:   keyAuraPay,
		Label: "AuraPay",
		Note:  "Карты, СБП · ₽",
		Fields: []Field{
			{Key: "api_key", Label: "API-ключ", Kind: FieldSecret},
			{Key: "shop_id", Label: "Shop ID (UUID кассы)", Kind: FieldText},
			{Key: "method", Label: "Метод оплаты", Kind: FieldSelect, Optional: true,
				Options: []FieldOption{
					{Value: "", Label: "Все методы (выбор на странице)"},
					{Value: "sbp", Label: "СБП"},
					{Value: "card", Label: "Карты"},
				},
				Help: "Пусто — клиент выбирает метод на странице AuraPay."},
		},
		New: func(cfg Config) Client {
			return &AuraPay{apiKey: cfg.Get("api_key"), shopID: cfg.Get("shop_id"), method: cfg.Get("method")}
		},
	}
}

// AuraPay is a minimal AuraPay client.
type AuraPay struct {
	apiKey string
	shopID string
	method string
	base   string // overridable in tests
}

func (a *AuraPay) endpoint() string {
	if a.base != "" {
		return a.base
	}
	return auraPayAPI
}

func (a *AuraPay) headers() map[string]string {
	return map[string]string{"X-ApiKey": a.apiKey, "X-ShopId": a.shopID}
}

// Create opens an invoice and returns its id plus the hosted pay URL. The payment
// method is left unset so the payer picks it on the AuraPay page.
func (a *AuraPay) Create(ctx context.Context, req CreateReq) (string, string, error) {
	body := map[string]any{
		"amount":   req.AmountRub, // whole rubles
		"order_id": fmt.Sprintf("%d", req.OrderID),
		"comment":  req.Description,
		"lifetime": 60,
	}
	// No method set ⇒ let the payer pick on the AuraPay page.
	if a.method != "" {
		body["service"] = a.method
	}
	if req.ReturnURL != "" {
		body["success_url"] = req.ReturnURL
		body["fail_url"] = req.ReturnURL
	}
	if req.WebhookURL != "" {
		body["callback_url"] = req.WebhookURL
	}
	var out struct {
		ID          string `json:"id"`
		PaymentData struct {
			URL string `json:"url"`
		} `json:"payment_data"`
		Error string `json:"error"`
	}
	if err := callJSON(ctx, "AuraPay", http.MethodPost, a.endpoint()+"/invoice/create", a.headers(), body, &out); err != nil {
		return "", "", err
	}
	if out.ID == "" || out.PaymentData.URL == "" {
		return "", "", fmt.Errorf("AuraPay: пустой ответ при создании счёта: %s", out.Error)
	}
	return out.ID, out.PaymentData.URL, nil
}

// Status re-reads an invoice by its id.
func (a *AuraPay) Status(ctx context.Context, providerID string) (Result, error) {
	var out struct {
		Status   string  `json:"status"`
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	}
	if err := callJSON(ctx, "AuraPay", http.MethodPost, a.endpoint()+"/invoice/status", a.headers(), map[string]any{"id": providerID}, &out); err != nil {
		return Result{}, err
	}
	return auraPayStatus(out.Status, out.Amount, out.Currency), nil
}

// Webhook doesn't trust the callback body: it re-fetches over the authenticated API.
func (a *AuraPay) Webhook(ctx context.Context, body []byte, _ http.Header) (string, Result, error) {
	var n struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &n) != nil || n.ID == "" {
		return "", Result{}, fmt.Errorf("AuraPay: некорректное уведомление")
	}
	res, err := a.Status(ctx, n.ID)
	if err != nil {
		return "", Result{}, err
	}
	return n.ID, res, nil
}

func auraPayStatus(status string, amount float64, currency string) Result {
	if currency == "" {
		currency = "RUB"
	}
	switch status {
	case "PAID":
		return amountResult(StatusPaid, decimalRub(amount), currency)
	case "EXPIRED":
		return Result{Status: StatusCanceled}
	default: // PENDING
		return Result{Status: StatusPending}
	}
}
