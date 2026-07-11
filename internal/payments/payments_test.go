package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- CryptoBot webhook signature (no network) -----------------------------

func cryptoSig(token string, body []byte) string {
	secret := sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestCryptoBotVerifyWebhook(t *testing.T) {
	c := NewCryptoBot("apptoken:123", false)
	body := []byte(`{"update_type":"invoice_paid","payload":{"invoice_id":7,"status":"paid"}}`)
	good := cryptoSig("apptoken:123", body)

	if !c.VerifyWebhook(body, good) {
		t.Fatal("valid signature rejected")
	}
	if c.VerifyWebhook(body, good+"00") {
		t.Fatal("wrong-length signature accepted")
	}
	if c.VerifyWebhook(body, strings.Repeat("0", len(good))) {
		t.Fatal("forged signature accepted")
	}
	// A single tampered byte in the body must invalidate the original signature.
	tampered := append([]byte{}, body...)
	tampered[10] ^= 0xff
	if c.VerifyWebhook(tampered, good) {
		t.Fatal("signature valid for tampered body")
	}
	// A different token must not validate.
	other := NewCryptoBot("different", false)
	if other.VerifyWebhook(body, good) {
		t.Fatal("signature validated under the wrong token")
	}
}

// --- CryptoBot API calls (httptest) ---------------------------------------

func TestCryptoBotCreateInvoice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/createInvoice" {
			t.Errorf("path = %q, want /createInvoice", r.URL.Path)
		}
		if got := r.Header.Get("Crypto-Pay-API-Token"); got != "tok" {
			t.Errorf("token header = %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"invoice_id":4242,"status":"active","pay_url":"https://t.me/pay"}}`))
	}))
	defer srv.Close()

	c := &CryptoBot{token: "tok", http: srv.Client(), baseURL: srv.URL}
	id, url, err := c.CreateInvoice(context.Background(), 100, 7, "Тариф")
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if id != "4242" || url != "https://t.me/pay" {
		t.Fatalf("got id=%q url=%q", id, url)
	}
}

func TestCryptoBotCreateInvoiceAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":400,"name":"AMOUNT_INVALID"}}`))
	}))
	defer srv.Close()

	c := &CryptoBot{token: "tok", http: srv.Client(), baseURL: srv.URL}
	if _, _, err := c.CreateInvoice(context.Background(), 0, 1, ""); err == nil {
		t.Fatal("expected error for ok:false envelope")
	}
}

func TestCryptoBotInvoiceStatusMapping(t *testing.T) {
	cases := []struct {
		api  string
		want Status
	}{
		{"paid", StatusPaid},
		{"expired", StatusCanceled},
		{"active", StatusPending},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"items":[{"status":"` + tc.api + `"}]}}`))
		}))
		c := &CryptoBot{token: "tok", http: srv.Client(), baseURL: srv.URL}
		got, err := c.InvoiceStatus(context.Background(), "1")
		srv.Close()
		if err != nil || got.Status != tc.want {
			t.Fatalf("status %q → %q, want %q (err=%v)", tc.api, got.Status, tc.want, err)
		}
	}
	// No items (unknown invoice) → pending, not an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":{"items":[]}}`))
	}))
	defer srv.Close()
	c := &CryptoBot{token: "tok", http: srv.Client(), baseURL: srv.URL}
	if got, err := c.InvoiceStatus(context.Background(), "9"); err != nil || got.Status != StatusPending {
		t.Fatalf("empty items → %q (err=%v), want pending", got.Status, err)
	}
}

// --- YooKassa -------------------------------------------------------------

func TestYooKassaAuthHeader(t *testing.T) {
	y := NewYooKassa("shop1", "secret")
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("shop1:secret"))
	if y.auth() != want {
		t.Fatalf("auth = %q, want %q", y.auth(), want)
	}
}

func TestYooKassaCreatePayment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/payments" || r.Method != http.MethodPost {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" || r.Header.Get("Idempotence-Key") == "" {
			t.Error("missing auth or idempotence-key header")
		}
		_, _ = w.Write([]byte(`{"id":"pay_1","status":"pending","confirmation":{"confirmation_url":"https://yk/redirect"}}`))
	}))
	defer srv.Close()

	y := &YooKassa{shopID: "s", secretKey: "k", http: srv.Client(), base: srv.URL}
	id, url, err := y.CreatePayment(context.Background(), 250, 9, "Тариф", "https://t.me/")
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if id != "pay_1" || url != "https://yk/redirect" {
		t.Fatalf("got id=%q url=%q", id, url)
	}
}

func TestYooKassaCreatePaymentEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"","confirmation":{"confirmation_url":""}}`))
	}))
	defer srv.Close()
	y := &YooKassa{shopID: "s", secretKey: "k", http: srv.Client(), base: srv.URL}
	if _, _, err := y.CreatePayment(context.Background(), 1, 1, "", ""); err == nil {
		t.Fatal("expected error for empty confirmation url")
	}
}

func TestYooKassaPaymentStatusMapping(t *testing.T) {
	cases := []struct {
		api  string
		want Status
	}{
		{"succeeded", StatusPaid},
		{"canceled", StatusCanceled},
		{"pending", StatusPending},
		{"waiting_for_capture", StatusPending},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"` + tc.api + `","paid":true}`))
		}))
		y := &YooKassa{shopID: "s", secretKey: "k", http: srv.Client(), base: srv.URL}
		got, err := y.PaymentStatus(context.Background(), "pay_1")
		srv.Close()
		if err != nil || got.Status != tc.want {
			t.Fatalf("status %q → %q, want %q (err=%v)", tc.api, got.Status, tc.want, err)
		}
	}
}

func TestYooKassaHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","code":"invalid_credentials"}`))
	}))
	defer srv.Close()
	y := &YooKassa{shopID: "s", secretKey: "k", http: srv.Client(), base: srv.URL}
	if _, err := y.PaymentStatus(context.Background(), "pay_1"); err == nil {
		t.Fatal("expected error on HTTP 401")
	}
}

// --- amount parsing / extraction ------------------------------------------

func TestParseKopecks(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"100.00", 10000, true}, // YooKassa always sends 2 decimals
		{"100", 10000, true},    // CryptoBot sends a bare float string
		{"125.50", 12550, true},
		{"0.99", 99, true},
		{"100.5", 10050, true},   // 1 decimal digit → padded, not misread as 5 kopecks
		{"100.004", 10000, true}, // sub-kopeck noise truncated
		{" 250.00 ", 25000, true},
		{"", 0, false},
		{"abc", 0, false},
		{"-5.00", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseKopecks(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseKopecks(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// The amount must come from "amount" (what the payer was charged), never from
// income_amount (net of commission) — else every payment would look short.
func TestYooKassaPaymentStatusAmount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"succeeded","paid":true,` +
			`"amount":{"value":"100.00","currency":"RUB"},` +
			`"income_amount":{"value":"96.50","currency":"RUB"}}`))
	}))
	defer srv.Close()
	y := &YooKassa{shopID: "s", secretKey: "k", http: srv.Client(), base: srv.URL}
	got, err := y.PaymentStatus(context.Background(), "pay_1")
	if err != nil {
		t.Fatalf("PaymentStatus: %v", err)
	}
	if got.Status != StatusPaid || got.AmountKopecks != 10000 || got.Currency != "RUB" {
		t.Fatalf("got %+v, want paid/10000/RUB", got)
	}
}

// A fiat invoice reports the RUB amount in "amount"/"fiat"; paid_amount is the
// crypto actually sent and must not be mistaken for the charge.
func TestCryptoBotInvoiceStatusAmount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":{"items":[{"status":"paid",` +
			`"currency_type":"fiat","fiat":"RUB","amount":"250",` +
			`"paid_amount":"2.71","paid_asset":"USDT"}]}}`))
	}))
	defer srv.Close()
	c := &CryptoBot{token: "tok", http: srv.Client(), baseURL: srv.URL}
	got, err := c.InvoiceStatus(context.Background(), "1")
	if err != nil {
		t.Fatalf("InvoiceStatus: %v", err)
	}
	if got.Status != StatusPaid || got.AmountKopecks != 25000 || got.Currency != "RUB" {
		t.Fatalf("got %+v, want paid/25000/RUB", got)
	}
}

// An unreadable amount must report "unknown" (0/"") rather than a bogus 0 RUB, so
// the caller fails open instead of rejecting a real payment as a mismatch.
func TestResultUnknownAmountOnUnparseable(t *testing.T) {
	inv := Invoice{Status: "paid", Amount: "not-a-number", Fiat: "RUB"}
	got := inv.AsResult()
	if got.AmountKopecks != 0 || got.Currency != "" {
		t.Fatalf("got %+v, want unknown amount (0, \"\")", got)
	}
}
