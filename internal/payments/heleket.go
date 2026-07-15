package payments

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Heleket (ex-Cryptomus) is a crypto processor that can invoice in fiat: the
// invoice is priced in RUB and the payer sends the crypto equivalent, so from the
// panel's side it behaves like any other RUB provider. A merchant account needs no
// company registration — Heleket accepts individuals — which is why it's a good fit
// here.
//
// Signing: sign = md5( base64(request_body_bytes) + api_key ), sent in the "sign"
// header (Cryptomus-compatible). We sign the exact bytes we POST, so no JSON
// canonicalisation games are needed on our side. The webhook is NOT verified by
// reproducing that signature (which would mean re-encoding the received JSON
// byte-for-byte the way Heleket did — fragile across languages); instead the
// callback's order id is used to re-fetch the invoice over the signed API, and that
// authenticated answer is what's trusted. Same safety model as YooKassa.

const keyHeleket = "heleket"

const heleketAPI = "https://api.heleket.com/v1"

func heleketDescriptor() Descriptor {
	return Descriptor{
		Key:   keyHeleket,
		Label: "Heleket",
		Note:  "Крипта: USDT и др. · счёт в ₽",
		Fields: []Field{
			{Key: "merchant_id", Label: "Merchant UUID", Kind: FieldText,
				Help: "Личный кабинет Heleket → Merchant → UUID."},
			{Key: "api_key", Label: "Payment API key", Kind: FieldSecret},
			{Key: "to_currency", Label: "Валюта оплаты", Kind: FieldText, Optional: true,
				Placeholder: "USDT", Help: "Необязательно. Пусто — плательщик выбирает монету сам."},
		},
		New: func(cfg Config) Client {
			return &Heleket{merchant: cfg.Get("merchant_id"), apiKey: cfg.Get("api_key"), toCurrency: cfg.Get("to_currency")}
		},
	}
}

// Heleket is a minimal Heleket/Cryptomus payment client.
type Heleket struct {
	merchant   string
	apiKey     string
	toCurrency string
	base       string // overridable in tests
}

func (h *Heleket) endpoint() string {
	if h.base != "" {
		return h.base
	}
	return heleketAPI
}

// call POSTs a JSON body with the merchant + sign headers and decodes result.
func (h *Heleket) call(ctx context.Context, path string, body map[string]any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	sign := md5Hex(base64.StdEncoding.EncodeToString(raw) + h.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint()+path, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("merchant", h.merchant)
	req.Header.Set("sign", sign)
	return send("Heleket", req, out)
}

type heleketResult struct {
	State  int `json:"state"`
	Result struct {
		UUID     string `json:"uuid"`
		OrderID  string `json:"order_id"`
		URL      string `json:"url"`
		Status   string `json:"status"`
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"result"`
	Message string `json:"message"`
}

// Create opens a fiat (RUB) invoice; the payer settles it in crypto.
func (h *Heleket) Create(ctx context.Context, req CreateReq) (string, string, error) {
	body := map[string]any{
		"amount":       rubles(req.AmountRub),
		"currency":     "RUB",
		"order_id":     fmt.Sprintf("%d", req.OrderID),
		"url_callback": req.WebhookURL,
		"lifetime":     3600,
	}
	if req.ReturnURL != "" {
		body["url_return"] = req.ReturnURL
	}
	if h.toCurrency != "" {
		body["to_currency"] = strings.ToUpper(h.toCurrency)
	}
	var out heleketResult
	if err := h.call(ctx, "/payment", body, &out); err != nil {
		return "", "", err
	}
	if out.Result.UUID == "" || out.Result.URL == "" {
		return "", "", fmt.Errorf("Heleket: пустой ответ при создании счёта: %s", out.Message)
	}
	return out.Result.UUID, out.Result.URL, nil
}

// Status re-reads an invoice by its Heleket uuid.
func (h *Heleket) Status(ctx context.Context, providerID string) (Result, error) {
	var out heleketResult
	if err := h.call(ctx, "/payment/info", map[string]any{"uuid": providerID}, &out); err != nil {
		return Result{}, err
	}
	return heleketStatus(out.Result.Status, out.Result.Amount, out.Result.Currency), nil
}

// Webhook doesn't trust the POST body: it pulls the uuid out and re-fetches the
// invoice over the signed API, reporting that instead.
func (h *Heleket) Webhook(ctx context.Context, body []byte, _ http.Header) (string, Result, error) {
	var n struct {
		UUID    string `json:"uuid"`
		OrderID string `json:"order_id"`
	}
	if json.Unmarshal(body, &n) != nil || n.UUID == "" {
		return "", Result{}, fmt.Errorf("Heleket: некорректное уведомление")
	}
	res, err := h.Status(ctx, n.UUID)
	if err != nil {
		return "", Result{}, err
	}
	return n.UUID, res, nil
}

// heleketStatus maps a Heleket payment status. The full set is documented at
// doc.heleket.com/methods/payments/payment-statuses.
func heleketStatus(status, amount, currency string) Result {
	switch status {
	case "paid", "paid_over":
		return amountResult(StatusPaid, amount, currency)
	case "cancel", "fail", "system_fail", "wrong_amount",
		"locked", "refund_process", "refund_fail", "refund_paid":
		return Result{Status: StatusCanceled}
	default: // check, process, confirm_check, wrong_amount_waiting, …
		return Result{Status: StatusPending}
	}
}
