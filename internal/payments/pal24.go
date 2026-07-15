package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Pal24 is PayPalych's REST API (v1): a RUB card/SBP gateway with a plain Bearer
// token — no request signing. The webhook is a classic PayPalych postback (JSON or
// form-encoded) signed as MD5(OutSum:InvId:token), uppercase hex, in the
// SignatureValue field.

const keyPal24 = "pal24"

const pal24API = "https://pal24.pro/api/v1"

func pal24Descriptor() Descriptor {
	return Descriptor{
		Key:   keyPal24,
		Label: "PayPalych",
		Note:  "Карты, СБП · ₽",
		Fields: []Field{
			{Key: "api_token", Label: "API-токен", Kind: FieldSecret, Help: "Личный кабинет PayPalych → API."},
			{Key: "shop_id", Label: "Shop ID", Kind: FieldText},
			{Key: "signature_token", Label: "Токен подписи", Kind: FieldSecret, Optional: true,
				Help: "Необязательно. Если пусто — подпись вебхука проверяется по API-токену."},
		},
		New: func(cfg Config) Client {
			return &Pal24{apiToken: cfg.Get("api_token"), shopID: cfg.Get("shop_id"), signatureToken: cfg.Get("signature_token")}
		},
	}
}

// Pal24 is a minimal PayPalych client.
type Pal24 struct {
	apiToken       string
	shopID         string
	signatureToken string
	base           string // overridable in tests
}

func (p *Pal24) endpoint() string {
	if p.base != "" {
		return p.base
	}
	return pal24API
}

func (p *Pal24) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + p.apiToken}
}

// signToken is the key PayPalych signs the webhook with: the dedicated signature
// token if set, otherwise the API token.
func (p *Pal24) signToken() string {
	if p.signatureToken != "" {
		return p.signatureToken
	}
	return p.apiToken
}

// Create opens a bill and returns its id plus the hosted payment page.
func (p *Pal24) Create(ctx context.Context, req CreateReq) (string, string, error) {
	body := map[string]any{
		"amount":      rubles(req.AmountRub),
		"shop_id":     p.shopID,
		"currency_in": "RUB",
		"type":        "normal",
		"order_id":    fmt.Sprintf("%d", req.OrderID),
		"description": req.Description,
	}
	var out struct {
		Success     any    `json:"success"`
		BillID      string `json:"bill_id"`
		LinkURL     string `json:"link_url"`
		LinkPageURL string `json:"link_page_url"`
		TransferURL string `json:"transfer_url"`
		Message     string `json:"message"`
		Error       string `json:"error"`
	}
	if err := callJSON(ctx, "PayPalych", http.MethodPost, p.endpoint()+"/bill/create", p.headers(), body, &out); err != nil {
		return "", "", err
	}
	payURL := firstNonEmpty(out.LinkPageURL, out.LinkURL, out.TransferURL)
	if out.BillID == "" || payURL == "" {
		return "", "", fmt.Errorf("PayPalych: пустой ответ при создании счёта: %s%s", out.Message, out.Error)
	}
	return out.BillID, payURL, nil
}

// Status re-reads a bill by its id.
func (p *Pal24) Status(ctx context.Context, providerID string) (Result, error) {
	var out struct {
		Status string `json:"status"`
		Bill   struct {
			Status string `json:"status"`
		} `json:"bill"`
	}
	u := p.endpoint() + "/bill/status?" + url.Values{"id": {providerID}}.Encode()
	if err := callJSON(ctx, "PayPalych", http.MethodGet, u, p.headers(), nil, &out); err != nil {
		return Result{}, err
	}
	status := out.Status
	if status == "" {
		status = out.Bill.Status
	}
	// bill/status carries no amount to verify against; the amount is confirmed on the
	// signed webhook. Report an unknown amount so the caller fails open on a poll.
	return pal24Status(status, "", ""), nil
}

// Webhook parses the postback (JSON or form-encoded), checks the MD5 signature, and
// reports the bill's state.
func (p *Pal24) Webhook(_ context.Context, body []byte, h http.Header) (string, Result, error) {
	fields := parseBody(body, h.Get("Content-Type"))
	invID := firstNonEmpty(fields["InvId"], fields["InvID"], fields["order_id"])
	outSum := firstNonEmpty(fields["OutSum"], fields["out_sum"], fields["Amount"])
	sig := fields["SignatureValue"]
	billID := firstNonEmpty(fields["bill_id"], fields["BillId"], fields["BillID"])
	status := firstNonEmpty(fields["Status"], fields["status"])
	if invID == "" || outSum == "" || sig == "" {
		return "", Result{}, fmt.Errorf("PayPalych: неполное уведомление")
	}
	// PayPalych signs the OutSum string exactly as it sent it — don't reformat it.
	want := strings.ToUpper(md5Hex(outSum + ":" + invID + ":" + p.signToken()))
	if !eqSig(want, sig) {
		return "", Result{}, fmt.Errorf("PayPalych: неверная подпись")
	}
	// The order is keyed on bill_id (what Create stored). Never substitute our own
	// order id here: it isn't a bill_id, so the lookup would miss — or, worse, collide
	// with some other order whose bill_id happens to equal this number. A postback
	// without bill_id is left to the polling fallback (bill/status by bill_id).
	if billID == "" {
		return "", Result{}, fmt.Errorf("PayPalych: в уведомлении нет bill_id")
	}
	return billID, pal24Status(status, outSum, "RUB"), nil
}

func pal24Status(status, amount, currency string) Result {
	switch strings.ToUpper(status) {
	case "PAID", "SUCCESS", "OVERPAID":
		return amountResult(StatusPaid, amount, currency)
	case "FAIL", "CANCELLED", "CANCELED":
		return Result{Status: StatusCanceled}
	default: // NEW, PROCESS, UNDERPAID, ""
		return Result{Status: StatusPending}
	}
}

// parseBody reads a webhook body that may be JSON or application/x-www-form-urlencoded
// into a flat string map (PayPalych sends whichever). JSON values are kept in their
// exact source representation (json.RawMessage): PayPalych signs OutSum byte-for-byte
// as it appears on the wire, so a numeric "150.00" must not be reformatted to "150".
func parseBody(body []byte, contentType string) map[string]string {
	out := map[string]string{}
	if strings.Contains(contentType, "application/json") || (len(body) > 0 && body[0] == '{') {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(body, &m); err == nil {
			for k, raw := range m {
				out[k] = rawJSONToString(raw)
			}
			return out
		}
	}
	if vals, err := url.ParseQuery(string(body)); err == nil {
		for k := range vals {
			out[k] = vals.Get(k)
		}
	}
	return out
}

// rawJSONToString unquotes a JSON string value but leaves numbers/bools/null in
// their exact source bytes, so signature fields survive round-tripping unchanged.
func rawJSONToString(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 0 && s[0] == '"' {
		var str string
		if json.Unmarshal(raw, &str) == nil {
			return str
		}
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
