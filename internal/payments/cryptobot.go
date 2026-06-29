package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CryptoBot is a minimal Crypto Pay API client. testnet switches to the
// @CryptoTestnetBot sandbox endpoint (use a testnet app token).
type CryptoBot struct {
	token   string
	testnet bool
	http    *http.Client
	baseURL string // overrides the endpoint in tests; empty → real Crypto Pay API
}

func NewCryptoBot(token string, testnet bool) *CryptoBot {
	return &CryptoBot{token: token, testnet: testnet, http: httpClient()}
}

func (c *CryptoBot) base() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	if c.testnet {
		return "https://testnet-pay.crypt.bot/api"
	}
	return "https://pay.crypt.bot/api"
}

// CreateInvoice creates a fiat (RUB) invoice — the user pays the crypto
// equivalent — and returns its id + the pay URL to open in Telegram.
func (c *CryptoBot) CreateInvoice(ctx context.Context, amountRub int, orderID int64, description string) (invoiceID, payURL string, err error) {
	body := map[string]any{
		"currency_type": "fiat",
		"fiat":          "RUB",
		"amount":        fmt.Sprintf("%d", amountRub),
		"description":   description,
		"payload":       fmt.Sprintf("order:%d", orderID),
		"expires_in":    3600,
	}
	var out struct {
		Result struct {
			InvoiceID int64  `json:"invoice_id"`
			Status    string `json:"status"`
			PayURL    string `json:"pay_url"`
			BotURL    string `json:"bot_invoice_url"`
			MiniURL   string `json:"mini_app_invoice_url"`
		} `json:"result"`
	}
	if err := c.call(ctx, "createInvoice", body, &out); err != nil {
		return "", "", err
	}
	url := out.Result.PayURL
	if url == "" {
		url = out.Result.BotURL
	}
	if out.Result.InvoiceID == 0 || url == "" {
		return "", "", fmt.Errorf("CryptoBot: пустой ответ при создании счёта")
	}
	return fmt.Sprintf("%d", out.Result.InvoiceID), url, nil
}

// InvoiceStatus returns the normalised status of an invoice.
func (c *CryptoBot) InvoiceStatus(ctx context.Context, invoiceID string) (Status, error) {
	body := map[string]any{"invoice_ids": invoiceID}
	var out struct {
		Result struct {
			Items []struct {
				Status string `json:"status"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := c.call(ctx, "getInvoices", body, &out); err != nil {
		return "", err
	}
	if len(out.Result.Items) == 0 {
		return StatusPending, nil
	}
	switch out.Result.Items[0].Status {
	case "paid":
		return StatusPaid, nil
	case "expired":
		return StatusCanceled, nil
	default: // active
		return StatusPending, nil
	}
}

// VerifyWebhook checks the crypto-pay-api-signature header: HMAC-SHA256 of the raw
// body keyed by SHA256(token), hex-encoded.
func (c *CryptoBot) VerifyWebhook(body []byte, signature string) bool {
	secret := sha256.Sum256([]byte(c.token))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signature))
}

func (c *CryptoBot) call(ctx context.Context, method string, body any, out any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Crypto-Pay-API-Token", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("CryptoBot: HTTP %d: %s", resp.StatusCode, string(data))
	}
	var envelope struct {
		OK    bool            `json:"ok"`
		Error json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(data, &envelope)
	if !envelope.OK {
		return fmt.Errorf("CryptoBot: ошибка API: %s", string(envelope.Error))
	}
	return json.Unmarshal(data, out)
}
