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

// YooKassa is a minimal Checkout API client (create payment + status).
type YooKassa struct {
	shopID    string
	secretKey string
	http      *http.Client
}

func NewYooKassa(shopID, secretKey string) *YooKassa {
	return &YooKassa{shopID: shopID, secretKey: secretKey, http: httpClient()}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, yooKassaAPI+"/payments", bytes.NewReader(raw))
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

// PaymentStatus returns the normalised status of a payment.
func (y *YooKassa) PaymentStatus(ctx context.Context, paymentID string) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, yooKassaAPI+"/payments/"+paymentID, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", y.auth())
	var out struct {
		Status string `json:"status"`
		Paid   bool   `json:"paid"`
	}
	if err := y.do(req, &out); err != nil {
		return "", err
	}
	switch out.Status {
	case "succeeded":
		return StatusPaid, nil
	case "canceled":
		return StatusCanceled, nil
	default: // pending, waiting_for_capture
		return StatusPending, nil
	}
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
