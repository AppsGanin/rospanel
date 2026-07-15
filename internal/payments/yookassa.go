package payments

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
)

// yooKassaAPI is the single production endpoint. YooKassa has no separate sandbox
// host — test payments are made with TEST-shop credentials against this same URL,
// which is what the "test" toggle in the panel documents.
const yooKassaAPI = "https://api.yookassa.ru/v3"

const keyYooKassa = "yookassa"

func yooKassaDescriptor() Descriptor {
	return Descriptor{
		Key:   keyYooKassa,
		Label: "ЮКасса",
		Note:  "Карты, СБП · ₽",
		Fields: []Field{
			{Key: "shop_id", Label: "shopId", Kind: FieldText},
			{Key: "secret_key", Label: "Секретный ключ", Kind: FieldSecret, Placeholder: "live_… или test_…"},
			{Key: "test", Label: "Тестовый магазин", Kind: FieldBool,
				Help: "У ЮКассы нет отдельного адреса песочницы — тестовый режим включается тестовыми ключами."},
		},
		New: func(cfg Config) Client { return NewYooKassa(cfg.Get("shop_id"), cfg.Get("secret_key")) },
	}
}

// Create implements Client.
func (y *YooKassa) Create(ctx context.Context, req CreateReq) (string, string, error) {
	return y.CreatePayment(ctx, req.AmountRub, req.OrderID, req.Description, req.ReturnURL)
}

// Status implements Client.
func (y *YooKassa) Status(ctx context.Context, providerID string) (Result, error) {
	return y.PaymentStatus(ctx, providerID)
}

// Webhook implements Client. YooKassa signs nothing, so the POST body is not
// trusted for anything but the payment id: the payment is re-fetched over the
// authenticated API and that answer is what gets reported.
func (y *YooKassa) Webhook(ctx context.Context, body []byte, _ http.Header) (string, Result, error) {
	var n struct {
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if json.Unmarshal(body, &n) != nil || n.Object.ID == "" {
		return "", Result{}, fmt.Errorf("ЮКасса: некорректное уведомление")
	}
	res, err := y.PaymentStatus(ctx, n.Object.ID)
	if err != nil {
		return "", Result{}, err
	}
	return n.Object.ID, res, nil
}

// YooKassa is a minimal Checkout API client (create payment + status).
type YooKassa struct {
	shopID    string
	secretKey string
	http      *http.Client
	base      string // API base URL; overridable in tests (default yooKassaAPI)
}

func NewYooKassa(shopID, secretKey string) *YooKassa {
	return &YooKassa{shopID: shopID, secretKey: secretKey, http: httpClient(), base: yooKassaAPI}
}

func (y *YooKassa) auth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(y.shopID+":"+y.secretKey))
}

// CreatePayment creates an auto-capture payment and returns its id + the hosted
// confirmation URL to send the user to. amountRub is whole rubles.
func (y *YooKassa) CreatePayment(ctx context.Context, amountRub int, orderID int64, description, returnURL string) (paymentID, confirmURL string, err error) {
	body := map[string]any{
		"amount":       map[string]string{"value": fmt.Sprintf("%d.00", amountRub), "currency": "RUB"},
		"capture":      true,
		"confirmation": map[string]string{"type": "redirect", "return_url": returnURL},
		"description":  description,
		"metadata":     map[string]string{"order_id": fmt.Sprintf("%d", orderID)},
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, y.base+"/payments", bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", y.auth())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotence-Key", uuid.NewString())

	var out struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		Confirmation struct {
			ConfirmationURL string `json:"confirmation_url"`
		} `json:"confirmation"`
		Description string `json:"description"` // present on error too
	}
	if err := y.do(req, &out); err != nil {
		return "", "", err
	}
	if out.ID == "" || out.Confirmation.ConfirmationURL == "" {
		return "", "", fmt.Errorf("ЮКасса: пустой ответ при создании платежа")
	}
	return out.ID, out.Confirmation.ConfirmationURL, nil
}

// PaymentStatus returns the normalised status of a payment plus the amount
// YooKassa recorded for it. "amount" is what the payer was charged (a decimal
// string like "100.00"); note this is deliberately NOT income_amount, which is the
// net after commission and would never match the order.
func (y *YooKassa) PaymentStatus(ctx context.Context, paymentID string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, y.base+"/payments/"+paymentID, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", y.auth())
	var out struct {
		Status string `json:"status"`
		Paid   bool   `json:"paid"`
		Amount struct {
			Value    string `json:"value"`
			Currency string `json:"currency"`
		} `json:"amount"`
	}
	if err := y.do(req, &out); err != nil {
		return Result{}, err
	}
	res := Result{Currency: out.Amount.Currency}
	if k, ok := parseKopecks(out.Amount.Value); ok {
		res.AmountKopecks = k
	} else {
		res.Currency = "" // unreadable amount → report "unknown", not a bogus 0 RUB
	}
	switch out.Status {
	case "succeeded":
		res.Status = StatusPaid
	case "canceled":
		res.Status = StatusCanceled
	default: // pending, waiting_for_capture
		res.Status = StatusPending
	}
	return res, nil
}

func (y *YooKassa) do(req *http.Request, out any) error {
	resp, err := y.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ЮКасса: HTTP %d: %s", resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, out)
}
