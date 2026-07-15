package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

// SeverPay is a RUB card/SBP gateway. Every request is signed: mid + a per-request
// salt are added and the body is HMAC-SHA256'd (sorted, compact JSON). The webhook
// is NOT verified by reproducing its signature — SeverPay signs the whole callback
// JSON with the nested object left in arrival order, which is fragile to reproduce
// byte-for-byte in another language. Instead the callback's id is used to re-fetch
// the payment over the signed status API (payin/get), and that authenticated answer
// is trusted. SeverPay has a real status endpoint, so the polling fallback works too.

const keySeverPay = "severpay"

const severPayAPI = "https://severpay.io/api/merchant"

func severPayDescriptor() Descriptor {
	return Descriptor{
		Key:   keySeverPay,
		Label: "SeverPay",
		Note:  "Карты, СБП · ₽",
		Fields: []Field{
			{Key: "mid", Label: "Merchant ID (MID)", Kind: FieldText},
			{Key: "token", Label: "Токен (ключ подписи)", Kind: FieldSecret},
		},
		New: func(cfg Config) Client { return &SeverPay{mid: cfg.Get("mid"), token: cfg.Get("token")} },
	}
}

// SeverPay is a minimal SeverPay client.
type SeverPay struct {
	mid   string
	token string
	base  string // overridable in tests
}

func (s *SeverPay) endpoint() string {
	if s.base != "" {
		return s.base
	}
	return severPayAPI
}

// sign stamps mid + a fresh salt onto the body and sets the HMAC-SHA256 signature
// over the sorted, compact JSON of the body (sign excluded). We sign exactly what we
// send, so the request is self-consistent regardless of SeverPay's own serialiser.
func (s *SeverPay) sign(body map[string]any) {
	mid, _ := strconv.Atoi(s.mid)
	body["mid"] = mid
	body["salt"] = uuid.NewString()
	delete(body, "sign")
	body["sign"] = hmacSHA256Hex(s.token, string(canonicalJSON(body)))
}

type severPayResp struct {
	Status bool   `json:"status"`
	Msg    string `json:"msg"`
	Data   struct {
		ID       int64   `json:"id"`
		UID      string  `json:"uid"`
		URL      string  `json:"url"`
		Status   string  `json:"status"`
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	} `json:"data"`
}

// Create opens a payin. SeverPay requires a payer email; we synthesise one from the
// order when the caller has none (the panel doesn't collect payer emails).
func (s *SeverPay) Create(ctx context.Context, req CreateReq) (string, string, error) {
	email := req.Email
	if email == "" {
		email = fmt.Sprintf("pay%d@rospanel.pay", req.OrderID)
	}
	body := map[string]any{
		"order_id":     fmt.Sprintf("%d", req.OrderID),
		"amount":       req.AmountRub, // whole rubles as an integer (no float ambiguity in the sign)
		"currency":     "RUB",
		"client_email": email,
		"client_id":    fmt.Sprintf("%d", req.OrderID),
	}
	if req.ReturnURL != "" {
		body["url_return"] = req.ReturnURL
	}
	s.sign(body)
	var out severPayResp
	if err := callJSON(ctx, "SeverPay", http.MethodPost, s.endpoint()+"/payin/create", nil, body, &out); err != nil {
		return "", "", err
	}
	if !out.Status || out.Data.ID == 0 || out.Data.URL == "" {
		return "", "", fmt.Errorf("SeverPay: не удалось создать платёж: %s", out.Msg)
	}
	return fmt.Sprintf("%d", out.Data.ID), out.Data.URL, nil
}

// Status re-reads a payin by its SeverPay numeric id.
func (s *SeverPay) Status(ctx context.Context, providerID string) (Result, error) {
	id, err := strconv.ParseInt(providerID, 10, 64)
	if err != nil {
		return Result{}, fmt.Errorf("SeverPay: некорректный id платежа %q", providerID)
	}
	body := map[string]any{"id": id}
	s.sign(body)
	var out severPayResp
	if err := callJSON(ctx, "SeverPay", http.MethodPost, s.endpoint()+"/payin/get", nil, body, &out); err != nil {
		return Result{}, err
	}
	if !out.Status {
		return Result{}, fmt.Errorf("SeverPay: статус недоступен: %s", out.Msg)
	}
	return severStatus(out.Data.Status, out.Data.Amount, out.Data.Currency), nil
}

// Webhook doesn't trust the callback body: it pulls the id and re-fetches over the
// signed status API, reporting that.
func (s *SeverPay) Webhook(ctx context.Context, body []byte, _ http.Header) (string, Result, error) {
	var n struct {
		Type string `json:"type"`
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &n) != nil || n.Data.ID == 0 {
		return "", Result{}, fmt.Errorf("SeverPay: некорректное уведомление")
	}
	res, err := s.Status(ctx, fmt.Sprintf("%d", n.Data.ID))
	if err != nil {
		return "", Result{}, err
	}
	return fmt.Sprintf("%d", n.Data.ID), res, nil
}

func severStatus(status string, amount float64, currency string) Result {
	if currency == "" {
		currency = "RUB"
	}
	switch status {
	case "success":
		return amountResult(StatusPaid, strconv.FormatFloat(amount, 'f', 2, 64), currency)
	case "decline", "fail":
		return Result{Status: StatusCanceled}
	default: // new, process
		return Result{Status: StatusPending}
	}
}
