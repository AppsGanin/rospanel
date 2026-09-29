package payments

import (
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // YooMoney signs its notifications with SHA-1
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// YooMoney (ex-Yandex.Money) takes a transfer to a personal wallet through its
// quickpay form: no company, no contract — a wallet and a notification secret. The
// payer pays by card or from a YooMoney wallet; YooMoney posts an HTTP notification
// signed with SHA-1 over a fixed field list and the secret.
//
// There is no status API without an OAuth app, so the notification is the only
// confirmation (the 24 h sweep cancels an order that never gets one; money arriving
// later still claims it).

const keyYooMoney = "yoomoney"

const (
	yooMoneyQuickpay = "https://yoomoney.ru/quickpay/confirm"
	yooMoneyRUB      = "643"
)

func yooMoneyDescriptor() Descriptor {
	return Descriptor{
		Key:   keyYooMoney,
		Label: "ЮMoney",
		Note:  "payNote.yoomoney",
		Fields: []Field{
			{Key: "wallet", Label: "payField.yoomoneyWallet", Kind: FieldText, Placeholder: "4100…",
				Help: "payHelp.yoomoneyWallet"},
			{Key: "secret", Label: "payField.yoomoneySecret", Kind: FieldSecret,
				Help: "payHelp.yoomoneySecret"},
			{Key: "method", Label: "payField.method", Kind: FieldSelect, Optional: true, Options: []FieldOption{
				{Value: "AC", Label: "payField.cards"},
				{Value: "PC", Label: "payField.yoomoneyWalletPay"},
			}},
		},
		New: func(cfg Config) Client {
			return &YooMoney{wallet: cfg.Get("wallet"), secret: cfg.Get("secret"), method: cfg.Get("method")}
		},
	}
}

// YooMoney is a quickpay client.
type YooMoney struct {
	wallet string
	secret string
	method string // AC (card) or PC (YooMoney wallet); "" = AC
	base   string // overrides the form URL in tests
}

// Create builds the payment link. The label carries our order id plus a random
// tail: a wallet may take transfers from more than one shop, and the label is what
// the notification names.
func (y *YooMoney) Create(ctx context.Context, req CreateReq) (string, string, error) {
	var tail [4]byte
	_, _ = rand.Read(tail[:])
	label := fmt.Sprintf("rp%d-%s", req.OrderID, hex.EncodeToString(tail[:]))
	method := y.method
	if method != "PC" {
		method = "AC"
	}
	form := url.Values{}
	form.Set("receiver", y.wallet)
	form.Set("quickpay-form", "button")
	form.Set("paymentType", method)
	form.Set("sum", strconv.Itoa(req.AmountRub))
	form.Set("label", label)
	form.Set("targets", truncRunes(req.Description, 150))
	if req.ReturnURL != "" {
		form.Set("successURL", req.ReturnURL)
	}
	base := y.base
	if base == "" {
		base = yooMoneyQuickpay
	}
	// The form answers a redirect to the hosted payment page; that page is the link.
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, base, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	hreq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c := httpClient()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(hreq)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode/100 == 3 && loc != "" {
		if u, err := resp.Request.URL.Parse(loc); err == nil {
			return label, u.String(), nil
		}
	}
	if resp.StatusCode >= 400 {
		return "", "", &httpErr{provider: "YooMoney", code: resp.StatusCode}
	}
	// No redirect: the form link itself opens the same page.
	return label, base + "?" + form.Encode(), nil
}

// Status implements Client: YooMoney has no status API for a quickpay transfer.
func (y *YooMoney) Status(context.Context, string) (Result, error) {
	return Result{}, ErrNoStatusAPI
}

// Webhook checks the notification's SHA-1 and reports the transfer. A protected
// transfer (codepro) or one held until the payer confirms it (unaccepted) is not
// money on the wallet yet.
func (y *YooMoney) Webhook(_ context.Context, body []byte, _ http.Header) (string, Result, error) {
	f, err := url.ParseQuery(string(body))
	if err != nil {
		return "", Result{}, fmt.Errorf("YooMoney: malformed notification")
	}
	signed := strings.Join([]string{
		f.Get("notification_type"), f.Get("operation_id"), f.Get("amount"), f.Get("currency"),
		f.Get("datetime"), f.Get("sender"), f.Get("codepro"), y.secret, f.Get("label"),
	}, "&")
	sum := sha1.Sum([]byte(signed)) //nolint:gosec // provider-mandated signature algorithm
	if y.secret == "" || !eqSig(hex.EncodeToString(sum[:]), f.Get("sha1_hash")) {
		return "", Result{}, fmt.Errorf("YooMoney: bad signature")
	}
	label := f.Get("label")
	if label == "" {
		return "", Result{}, fmt.Errorf("YooMoney: the notification carries no label")
	}
	res := Result{Status: StatusPaid}
	if f.Get("codepro") == "true" || f.Get("unaccepted") == "true" {
		res.Status = StatusPending
	}
	// withdraw_amount is what the payer was charged — the order's sum; amount is what
	// reached the wallet after YooMoney's fee.
	if k, ok := parseKopecks(f.Get("withdraw_amount")); ok && f.Get("currency") == yooMoneyRUB {
		res.AmountKopecks, res.Currency = k, "RUB"
	}
	return label, res, nil
}

// truncRunes cuts s to at most n characters.
func truncRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
