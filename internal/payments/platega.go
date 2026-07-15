package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Platega is a RUB gateway (SBP, cards, crypto). Auth is a merchant id + secret in
// headers; there is no request signing. The webhook carries no signature (Platega
// echoes the merchant headers instead), so it's confirmed the robust way: pull the
// transaction id from the callback and re-fetch it over the authenticated status API.

const keyPlatega = "platega"

const plategaAPI = "https://app.platega.io"

func plategaDescriptor() Descriptor {
	return Descriptor{
		Key:   keyPlatega,
		Label: "Platega",
		Note:  "Карты, СБП, крипта · ₽",
		Fields: []Field{
			{Key: "merchant_id", Label: "Merchant ID", Kind: FieldText},
			{Key: "secret", Label: "Secret (API-ключ)", Kind: FieldSecret},
			{Key: "method", Label: "Метод оплаты", Kind: FieldSelect, Optional: true,
				Options: []FieldOption{
					{Value: "", Label: "Все методы (выбор на странице)"},
					{Value: "2", Label: "СБП / SberPay"},
					{Value: "11", Label: "Карты РФ"},
					{Value: "12", Label: "Межд. карты"},
					{Value: "13", Label: "Крипта"},
				},
				Help: "Пусто — клиент выбирает способ на странице Platega."},
		},
		New: func(cfg Config) Client {
			return &Platega{merchantID: cfg.Get("merchant_id"), secret: cfg.Get("secret"), method: cfg.Get("method")}
		},
	}
}

// Platega is a minimal Platega client.
type Platega struct {
	merchantID string
	secret     string
	method     string
	base       string // overridable in tests
}

func (p *Platega) endpoint() string {
	if p.base != "" {
		return p.base
	}
	return plategaAPI
}

func (p *Platega) headers() map[string]string {
	return map[string]string{"X-MerchantId": p.merchantID, "X-Secret": p.secret}
}

// Create opens a transaction and returns its id plus the hosted pay URL. With a
// specific method configured it creates a method-specific payment (v1); with none it
// uses the v2 endpoint that omits paymentMethod, so Platega shows its own method
// picker and the payer chooses card vs SBP on the payment page.
func (p *Platega) Create(ctx context.Context, req CreateReq) (string, string, error) {
	body := map[string]any{
		"paymentDetails": map[string]any{"amount": req.AmountRub, "currency": "RUB"},
		"description":    req.Description,
		"payload":        fmt.Sprintf("order:%d", req.OrderID),
	}
	if req.ReturnURL != "" {
		body["return"] = req.ReturnURL
		body["failedUrl"] = req.ReturnURL
	}
	path := "/v2/transaction/process" // no method ⇒ Platega's own method picker
	if m, err := strconv.Atoi(strings.TrimSpace(p.method)); err == nil && m > 0 {
		body["paymentMethod"] = m
		path = "/transaction/process"
	}
	var out struct {
		TransactionID string `json:"transactionId"`
		ID            string `json:"id"`
		Redirect      string `json:"redirect"` // v1 pay link
		URL           string `json:"url"`      // v2 pay link
		Message       string `json:"message"`
	}
	if err := callJSON(ctx, "Platega", http.MethodPost, p.endpoint()+path, p.headers(), body, &out); err != nil {
		return "", "", err
	}
	id := firstNonEmpty(out.TransactionID, out.ID)
	payURL := firstNonEmpty(out.Redirect, out.URL)
	if id == "" || payURL == "" {
		return "", "", fmt.Errorf("Platega: пустой ответ при создании платежа: %s", out.Message)
	}
	return id, payURL, nil
}

// Status re-reads a transaction by its id.
func (p *Platega) Status(ctx context.Context, providerID string) (Result, error) {
	var out struct {
		Status   string  `json:"status"`
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	}
	if err := callJSON(ctx, "Platega", http.MethodGet, p.endpoint()+"/transaction/"+providerID, p.headers(), nil, &out); err != nil {
		return Result{}, err
	}
	return plategaStatus(out.Status, out.Amount, out.Currency), nil
}

// Webhook doesn't trust the callback body: it re-fetches the transaction over the
// authenticated status API and reports that.
func (p *Platega) Webhook(ctx context.Context, body []byte, _ http.Header) (string, Result, error) {
	var n struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &n) != nil || n.ID == "" {
		return "", Result{}, fmt.Errorf("Platega: некорректное уведомление")
	}
	res, err := p.Status(ctx, n.ID)
	if err != nil {
		return "", Result{}, err
	}
	return n.ID, res, nil
}

func plategaStatus(status string, amount float64, currency string) Result {
	if currency == "" {
		currency = "RUB"
	}
	switch strings.ToUpper(status) {
	case "CONFIRMED":
		return amountResult(StatusPaid, decimalRub(amount), currency)
	case "FAILED", "CANCELED", "EXPIRED", "CHARGEBACKED":
		return Result{Status: StatusCanceled}
	default: // PENDING, INPROGRESS
		return Result{Status: StatusPending}
	}
}
