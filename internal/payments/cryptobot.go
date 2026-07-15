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

const keyCryptoBot = "cryptobot"

func cryptoBotDescriptor() Descriptor {
	return Descriptor{
		Key:   keyCryptoBot,
		Label: "CryptoBot",
		Note:  "Крипта: USDT, TON, BTC · счёт в ₽",
		Fields: []Field{
			{Key: "token", Label: "API-токен", Kind: FieldSecret, Placeholder: "12345:AA…",
				Help: "@CryptoBot → Crypto Pay → Create App."},
			{Key: "testnet", Label: "Testnet (@CryptoTestnetBot)", Kind: FieldBool},
		},
		New: func(cfg Config) Client { return NewCryptoBot(cfg.Get("token"), cfg.Bool("testnet")) },
	}
}

// Create implements Client.
func (c *CryptoBot) Create(ctx context.Context, req CreateReq) (string, string, error) {
	return c.CreateInvoice(ctx, req.AmountRub, req.OrderID, req.Description)
}

// Status implements Client.
func (c *CryptoBot) Status(ctx context.Context, providerID string) (Result, error) {
	return c.InvoiceStatus(ctx, providerID)
}

// Webhook implements Client. The HMAC proves CryptoBot sent the body, so the
// invoice it carries (including its amount) can be trusted as-is.
func (c *CryptoBot) Webhook(_ context.Context, body []byte, h http.Header) (string, Result, error) {
	if !c.VerifyWebhook(body, h.Get("crypto-pay-api-signature")) {
		return "", Result{}, fmt.Errorf("CryptoBot: неверная подпись")
	}
	var upd struct {
		UpdateType string `json:"update_type"`
		Payload    struct {
			Invoice
			InvoiceID int64 `json:"invoice_id"`
		} `json:"payload"`
	}
	if json.Unmarshal(body, &upd) != nil || upd.Payload.InvoiceID == 0 {
		return "", Result{}, fmt.Errorf("CryptoBot: некорректное уведомление")
	}
	res := upd.Payload.AsResult()
	if upd.UpdateType == "invoice_paid" {
		res.Status = StatusPaid // the update itself is the paid signal
	}
	return fmt.Sprintf("%d", upd.Payload.InvoiceID), res, nil
}

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

// InvoiceStatus returns the normalised status of an invoice plus the amount
// CryptoBot recorded for it. Invoices are created as fiat (RUB), so "amount" is
// the fiat amount (a decimal string) and "fiat" its currency — NOT paid_amount,
// which is the crypto the payer actually sent.
func (c *CryptoBot) InvoiceStatus(ctx context.Context, invoiceID string) (Result, error) {
	body := map[string]any{"invoice_ids": invoiceID}
	var out struct {
		Result struct {
			Items []Invoice `json:"items"`
		} `json:"result"`
	}
	if err := c.call(ctx, "getInvoices", body, &out); err != nil {
		return Result{}, err
	}
	if len(out.Result.Items) == 0 {
		return Result{Status: StatusPending}, nil
	}
	return out.Result.Items[0].AsResult(), nil
}

// Invoice is the subset of CryptoBot's Invoice object we consume. It is returned
// by getInvoices and is also the body of an invoice_paid webhook payload.
type Invoice struct {
	Status string `json:"status"`
	Amount string `json:"amount"` // fiat amount for a currency_type=fiat invoice
	Fiat   string `json:"fiat"`   // fiat currency code, e.g. "RUB"
}

// AsResult normalises an Invoice into a Result. Exported so a webhook payload
// (which is itself an Invoice) can be verified by the caller.
func (inv Invoice) AsResult() Result {
	res := Result{Currency: inv.Fiat}
	if k, ok := parseKopecks(inv.Amount); ok {
		res.AmountKopecks = k
	} else {
		res.Currency = "" // unreadable amount → report "unknown", not a bogus 0
	}
	switch inv.Status {
	case "paid":
		res.Status = StatusPaid
	case "expired":
		res.Status = StatusCanceled
	default: // active
		res.Status = StatusPending
	}
	return res
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
