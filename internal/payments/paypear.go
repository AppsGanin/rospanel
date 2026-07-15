package payments

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// PayPear (paypear.ru) is a RUB card/SBP gateway. Auth is HTTP Basic (shop id +
// secret key). Its webhook signature algorithm isn't publicly documented, so the
// callback isn't trusted: the payment id is pulled from it and re-fetched over the
// authenticated status API. PayPear may add a commission to the charged amount, so
// the amount is reported as unknown (the authenticated re-fetch plus the 1:1 id→order
// mapping already secure the confirmation).

const keyPayPear = "paypear"

const payPearAPI = "https://api.paypear.ru/v1"

func payPearDescriptor() Descriptor {
	return Descriptor{
		Key:   keyPayPear,
		Label: "PayPear",
		Note:  "Карты, СБП, SberPay · ₽",
		Fields: []Field{
			{Key: "shop_id", Label: "Shop ID", Kind: FieldText},
			{Key: "secret_key", Label: "Секретный ключ", Kind: FieldSecret},
			{Key: "method", Label: "Метод оплаты", Kind: FieldSelect, Optional: true,
				Options: []FieldOption{
					{Value: "", Label: "Все методы (выбор на странице)"},
					{Value: "sbp", Label: "СБП"},
					{Value: "bank_card", Label: "Карты"},
					{Value: "sberpay", Label: "SberPay"},
					{Value: "tpay", Label: "T-Pay"},
				},
				Help: "Пусто — клиент выбирает метод на странице PayPear."},
		},
		New: func(cfg Config) Client {
			return &PayPear{shopID: cfg.Get("shop_id"), secretKey: cfg.Get("secret_key"), method: cfg.Get("method")}
		},
	}
}

// PayPear is a minimal PayPear client.
type PayPear struct {
	shopID    string
	secretKey string
	method    string
	base      string // overridable in tests
}

func (p *PayPear) endpoint() string {
	if p.base != "" {
		return p.base
	}
	return payPearAPI
}

func (p *PayPear) headers() map[string]string {
	auth := base64.StdEncoding.EncodeToString([]byte(p.shopID + ":" + p.secretKey))
	return map[string]string{"Authorization": "Basic " + auth, "Idempotence-Key": uuid.NewString()}
}

// Create opens a payment and returns its id plus the hosted confirmation URL.
func (p *PayPear) Create(ctx context.Context, req CreateReq) (string, string, error) {
	body := map[string]any{
		"amount":      map[string]string{"value": rubles(req.AmountRub), "currency": "RUB"},
		"order_id":    fmt.Sprintf("%d", req.OrderID),
		"description": req.Description,
	}
	// No method set ⇒ let PayPear's page show every method and the payer choose.
	if method := strings.TrimSpace(p.method); method != "" {
		body["payment_method_data"] = map[string]string{"type": method}
	}
	if req.ReturnURL != "" {
		body["confirmation"] = map[string]string{"type": "redirect", "return_url": req.ReturnURL}
	}
	var out struct {
		Success bool `json:"success"`
		Result  struct {
			ID           string `json:"id"`
			Confirmation struct {
				ConfirmationURL string `json:"confirmation_url"`
			} `json:"confirmation"`
			URL string `json:"url"`
		} `json:"result"`
		Message string `json:"message"`
	}
	if err := callJSON(ctx, "PayPear", http.MethodPost, p.endpoint()+"/payment/", p.headers(), body, &out); err != nil {
		return "", "", err
	}
	payURL := firstNonEmpty(out.Result.Confirmation.ConfirmationURL, out.Result.URL)
	if !out.Success || out.Result.ID == "" || payURL == "" {
		return "", "", fmt.Errorf("PayPear: не удалось создать платёж: %s", out.Message)
	}
	return out.Result.ID, payURL, nil
}

// Status re-reads a payment by its id.
func (p *PayPear) Status(ctx context.Context, providerID string) (Result, error) {
	var out struct {
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	if err := callJSON(ctx, "PayPear", http.MethodGet, p.endpoint()+"/payment/"+providerID+"/", p.headers(), nil, &out); err != nil {
		return Result{}, err
	}
	return payPearStatus(out.Result.Status), nil
}

// Webhook doesn't trust the callback body: it re-fetches over the authenticated API.
func (p *PayPear) Webhook(ctx context.Context, body []byte, _ http.Header) (string, Result, error) {
	var n struct {
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if json.Unmarshal(body, &n) != nil || n.Object.ID == "" {
		return "", Result{}, fmt.Errorf("PayPear: некорректное уведомление")
	}
	res, err := p.Status(ctx, n.Object.ID)
	if err != nil {
		return "", Result{}, err
	}
	return n.Object.ID, res, nil
}

// payPearStatus maps a PayPear status. The amount is deliberately left unknown —
// PayPear may add a commission to the charged amount, so a strict match would reject
// real payments; the confirmation is already trusted (authenticated re-fetch).
func payPearStatus(status string) Result {
	switch strings.ToUpper(status) {
	case "CONFIRMED":
		return Result{Status: StatusPaid}
	case "CANCELED", "REFUNDED", "EXPIRED":
		return Result{Status: StatusCanceled}
	default: // NEW, PROCESS
		return Result{Status: StatusPending}
	}
}
