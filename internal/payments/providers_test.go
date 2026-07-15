package payments

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// --- registry --------------------------------------------------------------

func TestRegistryConfiguredAndLabel(t *testing.T) {
	d, ok := Get("heleket")
	if !ok {
		t.Fatal("heleket not in registry")
	}
	// Required fields missing → not configured; the optional one doesn't count.
	if d.Configured(Config{}) {
		t.Fatal("empty config must not be considered configured")
	}
	if !d.Configured(Config{"merchant_id": "m", "api_key": "k"}) {
		t.Fatal("required fields present should be configured")
	}
	if Label("heleket") != "Heleket" || Label("") != "вручную" || Label("nope") != "nope" {
		t.Fatalf("unexpected labels")
	}
}

// --- Heleket ---------------------------------------------------------------

func TestHeleketSignAndCreate(t *testing.T) {
	var gotSign, gotMerchant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSign = r.Header.Get("sign")
		gotMerchant = r.Header.Get("merchant")
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		// Recompute the signature the way Heleket's server does: md5(base64(body)+key).
		want := md5.Sum([]byte(base64.StdEncoding.EncodeToString(body) + "secret"))
		if gotSign != hex.EncodeToString(want[:]) {
			t.Errorf("sign header does not match md5(base64(body)+key)")
		}
		_, _ = w.Write([]byte(`{"state":0,"result":{"uuid":"u-1","url":"https://pay.heleket/u-1"}}`))
	}))
	defer srv.Close()

	h := &Heleket{merchant: "m-1", apiKey: "secret", base: srv.URL}
	id, payURL, err := h.Create(context.Background(), CreateReq{AmountRub: 150, OrderID: 7, WebhookURL: "https://x/cb"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "u-1" || payURL != "https://pay.heleket/u-1" {
		t.Fatalf("got id=%q url=%q", id, payURL)
	}
	if gotMerchant != "m-1" || gotSign == "" {
		t.Fatalf("headers not set: merchant=%q sign=%q", gotMerchant, gotSign)
	}
}

func TestHeleketStatusMapping(t *testing.T) {
	cases := map[string]Status{
		"paid": StatusPaid, "paid_over": StatusPaid,
		"cancel": StatusCanceled, "fail": StatusCanceled, "system_fail": StatusCanceled,
		"check": StatusPending, "process": StatusPending,
	}
	for api, want := range cases {
		got := heleketStatus(api, "150.00", "RUB")
		if got.Status != want {
			t.Errorf("heleketStatus(%q) = %q, want %q", api, got.Status, want)
		}
	}
	if got := heleketStatus("paid", "150.00", "RUB"); got.AmountKopecks != 15000 || got.Currency != "RUB" {
		t.Fatalf("paid amount = %+v, want 15000/RUB", got)
	}
}

// Heleket's webhook is verified by re-fetching, not by trusting the body.
func TestHeleketWebhookRefetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/payment/info" {
			t.Errorf("expected re-fetch to /payment/info, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"state":0,"result":{"status":"paid","amount":"150.00","currency":"RUB"}}`))
	}))
	defer srv.Close()
	h := &Heleket{merchant: "m", apiKey: "k", base: srv.URL}
	id, res, err := h.Webhook(context.Background(), []byte(`{"uuid":"u-1","order_id":"7","status":"paid"}`), http.Header{})
	if err != nil {
		t.Fatalf("Webhook: %v", err)
	}
	if id != "u-1" || res.Status != StatusPaid || res.AmountKopecks != 15000 {
		t.Fatalf("got id=%q res=%+v", id, res)
	}
}

// --- RioPay ----------------------------------------------------------------

func TestRioPayCreateAndAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Token") != "tok" {
			t.Errorf("missing X-Api-Token")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"550e","status":"CREATED","paymentLink":"https://riopay/550e"}`))
	}))
	defer srv.Close()
	r := &RioPay{token: "tok", base: srv.URL}
	id, url, err := r.Create(context.Background(), CreateReq{AmountRub: 100, OrderID: 5})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "550e" || url != "https://riopay/550e" {
		t.Fatalf("got id=%q url=%q", id, url)
	}
}

func TestRioPayWebhookSignature(t *testing.T) {
	body := []byte(`{"id":"550e","externalId":"5","status":"COMPLETED","amount":100.0,"currency":"RUB"}`)
	mac := hmac.New(sha512.New, []byte("tok"))
	mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))

	r := &RioPay{token: "tok"}
	// Valid signature (falls back to the API token when no webhook secret is set).
	id, res, err := r.Webhook(context.Background(), body, http.Header{"X-Signature": {good}})
	if err != nil || id != "550e" || res.Status != StatusPaid || res.AmountKopecks != 10000 {
		t.Fatalf("valid webhook: id=%q res=%+v err=%v", id, res, err)
	}
	// Forged signature rejected.
	if _, _, err := r.Webhook(context.Background(), body, http.Header{"X-Signature": {"deadbeef"}}); err == nil {
		t.Fatal("forged signature accepted")
	}
	// Missing signature rejected.
	if _, _, err := r.Webhook(context.Background(), body, http.Header{}); err == nil {
		t.Fatal("missing signature accepted")
	}
}

// --- Pal24 (PayPalych) -----------------------------------------------------

func pal24Sig(outSum, invID, token string) string {
	sum := md5.Sum([]byte(outSum + ":" + invID + ":" + token))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func TestPal24WebhookJSONAndForm(t *testing.T) {
	p := &Pal24{apiToken: "tok", shopID: "s"}
	sig := pal24Sig("150.00", "42", "tok")

	// JSON postback.
	jsonBody := `{"InvId":"42","OutSum":"150.00","Status":"SUCCESS","bill_id":"BILL-1","SignatureValue":"` + sig + `"}`
	id, res, err := p.Webhook(context.Background(), []byte(jsonBody), http.Header{"Content-Type": {"application/json"}})
	if err != nil || id != "BILL-1" || res.Status != StatusPaid || res.AmountKopecks != 15000 {
		t.Fatalf("json webhook: id=%q res=%+v err=%v", id, res, err)
	}

	// Form-encoded postback with the same fields.
	form := url.Values{"InvId": {"42"}, "OutSum": {"150.00"}, "Status": {"SUCCESS"}, "bill_id": {"BILL-1"}, "SignatureValue": {sig}}
	id, res, err = p.Webhook(context.Background(), []byte(form.Encode()), http.Header{"Content-Type": {"application/x-www-form-urlencoded"}})
	if err != nil || id != "BILL-1" || res.Status != StatusPaid {
		t.Fatalf("form webhook: id=%q res=%+v err=%v", id, res, err)
	}

	// Wrong signature rejected.
	bad := `{"InvId":"42","OutSum":"150.00","Status":"SUCCESS","SignatureValue":"AAAA"}`
	if _, _, err := p.Webhook(context.Background(), []byte(bad), http.Header{"Content-Type": {"application/json"}}); err == nil {
		t.Fatal("bad signature accepted")
	}

	// A postback without bill_id must fail closed (left to the poll), never reuse our
	// order id as a provider id.
	noBill := `{"InvId":"42","OutSum":"150.00","Status":"SUCCESS","SignatureValue":"` + sig + `"}`
	if _, _, err := p.Webhook(context.Background(), []byte(noBill), http.Header{"Content-Type": {"application/json"}}); err == nil {
		t.Fatal("postback without bill_id accepted")
	}
}

// PayPalych may send OutSum as a JSON number; the signature is over its exact
// on-the-wire text, so it must not be reformatted (150.00 → 150 would break it).
func TestPal24WebhookNumericOutSum(t *testing.T) {
	p := &Pal24{apiToken: "tok", shopID: "s"}
	sig := pal24Sig("150.00", "42", "tok")
	body := `{"InvId":42,"OutSum":150.00,"Status":"SUCCESS","bill_id":"BILL-1","SignatureValue":"` + sig + `"}`
	id, res, err := p.Webhook(context.Background(), []byte(body), http.Header{"Content-Type": {"application/json"}})
	if err != nil || id != "BILL-1" || res.Status != StatusPaid {
		t.Fatalf("numeric OutSum webhook: id=%q res=%+v err=%v", id, res, err)
	}
}

func TestPal24SignatureUsesSignatureToken(t *testing.T) {
	// When a dedicated signature token is set, the webhook must verify against it,
	// not the API token.
	p := &Pal24{apiToken: "api", signatureToken: "sigtok"}
	sig := pal24Sig("100.00", "9", "sigtok")
	body := `{"InvId":"9","OutSum":"100.00","Status":"PAID","bill_id":"B9","SignatureValue":"` + sig + `"}`
	if _, res, err := p.Webhook(context.Background(), []byte(body), http.Header{"Content-Type": {"application/json"}}); err != nil || res.Status != StatusPaid {
		t.Fatalf("signature-token webhook: res=%+v err=%v", res, err)
	}
}

// --- RollyPay --------------------------------------------------------------

func TestRollyPayWebhookSignature(t *testing.T) {
	body := []byte(`{"payment_id":"pay_1","event_type":"payment.paid","status":"paid","amount":"150.00","currency":"RUB"}`)
	ts := "1730000000"
	mac := hmac.New(sha256.New, []byte("sign-secret"))
	mac.Write([]byte(ts + "." + string(body)))
	good := hex.EncodeToString(mac.Sum(nil))

	r := &RollyPay{apiKey: "k", signingSecret: "sign-secret"}
	id, res, err := r.Webhook(context.Background(), body, http.Header{"X-Signature": {good}, "X-Timestamp": {ts}})
	if err != nil || id != "pay_1" || res.Status != StatusPaid || res.AmountKopecks != 15000 {
		t.Fatalf("valid webhook: id=%q res=%+v err=%v", id, res, err)
	}
	// Right signature but for a different timestamp must fail (ts is part of the MAC).
	if _, _, err := r.Webhook(context.Background(), body, http.Header{"X-Signature": {good}, "X-Timestamp": {"1730000001"}}); err == nil {
		t.Fatal("timestamp tampering accepted")
	}
	// Missing headers rejected.
	if _, _, err := r.Webhook(context.Background(), body, http.Header{}); err == nil {
		t.Fatal("missing signature accepted")
	}
}

// A payment.paid event with a lagging status field still gets its amount parsed for
// the cross-check, rather than confirming with an unknown amount (fail-open).
func TestRollyPayEventForcesPaidWithAmount(t *testing.T) {
	body := []byte(`{"payment_id":"pay_2","event_type":"payment.paid","status":"processing","amount":"150.00","currency":"RUB"}`)
	ts := "1730000000"
	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write([]byte(ts + "." + string(body)))
	sig := hex.EncodeToString(mac.Sum(nil))
	r := &RollyPay{apiKey: "k", signingSecret: "s"}
	id, res, err := r.Webhook(context.Background(), body, http.Header{"X-Signature": {sig}, "X-Timestamp": {ts}})
	if err != nil || id != "pay_2" || res.Status != StatusPaid || res.AmountKopecks != 15000 || res.Currency != "RUB" {
		t.Fatalf("event-forced paid: id=%q res=%+v err=%v", id, res, err)
	}
}

// --- SeverPay --------------------------------------------------------------

func TestCanonicalJSON(t *testing.T) {
	// Keys sorted, compact, and & / < not \u-escaped (must match the reference
	// python json.dumps(sorted, ensure_ascii=False, separators=(",",":"))).
	got := string(canonicalJSON(map[string]any{"b": 2, "a": "x&y", "c": "a<b"}))
	if got != `{"a":"x&y","b":2,"c":"a<b"}` {
		t.Fatalf("canonicalJSON = %s", got)
	}
}

func TestSeverPaySignStable(t *testing.T) {
	s := &SeverPay{mid: "77", token: "k"}
	body := map[string]any{"amount": 150, "order_id": "5"}
	s.sign(body)
	// The signature verifies against the canonical form of the body minus sign,
	// including the mid/salt the signer injected.
	sig, _ := body["sign"].(string)
	delete(body, "sign")
	if sig == "" || sig != hmacSHA256Hex("k", string(canonicalJSON(body))) {
		t.Fatalf("sign not reproducible over canonical body")
	}
	if body["mid"] != 77 || body["salt"] == nil {
		t.Fatalf("mid/salt not stamped: %+v", body)
	}
}

func TestSeverPayCreate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/payin/create" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got["sign"] == nil || got["mid"] == nil || got["client_email"] == nil {
			t.Errorf("missing sign/mid/email in body: %+v", got)
		}
		_, _ = w.Write([]byte(`{"status":true,"data":{"id":987654,"url":"https://severpay/pay/987654"}}`))
	}))
	defer srv.Close()
	s := &SeverPay{mid: "77", token: "k", base: srv.URL}
	id, url, err := s.Create(context.Background(), CreateReq{AmountRub: 150, OrderID: 5})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "987654" || url != "https://severpay/pay/987654" {
		t.Fatalf("got id=%q url=%q", id, url)
	}
}

func TestSeverPayStatusMapping(t *testing.T) {
	cases := map[string]Status{
		"success": StatusPaid, "decline": StatusCanceled, "fail": StatusCanceled,
		"new": StatusPending, "process": StatusPending,
	}
	for api, want := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":true,"data":{"status":"` + api + `","amount":150.0,"currency":"RUB"}}`))
		}))
		s := &SeverPay{mid: "77", token: "k", base: srv.URL}
		got, err := s.Status(context.Background(), "987654")
		srv.Close()
		if err != nil || got.Status != want {
			t.Fatalf("status %q → %q, want %q (err=%v)", api, got.Status, want, err)
		}
	}
}

// The webhook is confirmed by re-fetching the payment over the status API, not by
// trusting the callback body.
func TestSeverPayWebhookRefetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/payin/get" {
			t.Errorf("expected re-fetch to /payin/get, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":true,"data":{"status":"success","amount":150.0,"currency":"RUB"}}`))
	}))
	defer srv.Close()
	s := &SeverPay{mid: "77", token: "k", base: srv.URL}
	body := []byte(`{"type":"payin","data":{"id":987654,"order_id":"5","status":"success","amount":150.0},"sign":"whatever"}`)
	id, res, err := s.Webhook(context.Background(), body, http.Header{})
	if err != nil || id != "987654" || res.Status != StatusPaid || res.AmountKopecks != 15000 {
		t.Fatalf("webhook: id=%q res=%+v err=%v", id, res, err)
	}
}

// --- Platega / PayPear / AuraPay (webhook = re-fetch over status API) ------

func TestPlategaCreateAndWebhookRefetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-MerchantId") != "m" || r.Header.Get("X-Secret") != "s" {
			t.Errorf("missing auth headers")
		}
		switch r.URL.Path {
		case "/v2/transaction/process": // no method ⇒ picker page
			_, _ = w.Write([]byte(`{"transactionId":"tx-1","url":"https://pay.platega/tx-1","status":"PENDING"}`))
		case "/transaction/process": // method-specific
			_, _ = w.Write([]byte(`{"transactionId":"tx-1","redirect":"https://platega/tx-1","status":"PENDING"}`))
		case "/transaction/tx-1":
			_, _ = w.Write([]byte(`{"status":"CONFIRMED","amount":150,"currency":"RUB"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	// No method set → picker (v2), pay link in `url`.
	p := &Platega{merchantID: "m", secret: "s", base: srv.URL}
	id, url, err := p.Create(context.Background(), CreateReq{AmountRub: 150, OrderID: 5})
	if err != nil || id != "tx-1" || url != "https://pay.platega/tx-1" {
		t.Fatalf("create: id=%q url=%q err=%v", id, url, err)
	}
	// A specific method → v1 endpoint, pay link in `redirect`.
	pm := &Platega{merchantID: "m", secret: "s", method: "11", base: srv.URL}
	if _, u, err := pm.Create(context.Background(), CreateReq{AmountRub: 150, OrderID: 6}); err != nil || u != "https://platega/tx-1" {
		t.Fatalf("method create: url=%q err=%v", u, err)
	}
	// Webhook carries only {id}; confirmation comes from the re-fetched transaction.
	gotID, res, err := p.Webhook(context.Background(), []byte(`{"id":"tx-1","status":"CONFIRMED"}`), http.Header{})
	if err != nil || gotID != "tx-1" || res.Status != StatusPaid || res.AmountKopecks != 15000 {
		t.Fatalf("webhook: id=%q res=%+v err=%v", gotID, res, err)
	}
}

func TestPlategaStatusMapping(t *testing.T) {
	cases := map[string]Status{
		"CONFIRMED": StatusPaid, "PENDING": StatusPending, "INPROGRESS": StatusPending,
		"FAILED": StatusCanceled, "CANCELED": StatusCanceled, "EXPIRED": StatusCanceled,
	}
	for api, want := range cases {
		if got := plategaStatus(api, 150, "RUB"); got.Status != want {
			t.Errorf("platega %q → %q, want %q", api, got.Status, want)
		}
	}
}

func TestPayPearCreateAndWebhookRefetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			t.Errorf("missing basic auth")
		}
		switch {
		case r.URL.Path == "/payment/" && r.Method == http.MethodPost:
			if r.Header.Get("Idempotence-Key") == "" {
				t.Errorf("missing idempotence key")
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"pp-1","confirmation":{"confirmation_url":"https://pear/pp-1"}}}`))
		case r.URL.Path == "/payment/pp-1/":
			_, _ = w.Write([]byte(`{"result":{"status":"CONFIRMED"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	p := &PayPear{shopID: "shop", secretKey: "key", base: srv.URL}
	id, url, err := p.Create(context.Background(), CreateReq{AmountRub: 150, OrderID: 5})
	if err != nil || id != "pp-1" || url != "https://pear/pp-1" {
		t.Fatalf("create: id=%q url=%q err=%v", id, url, err)
	}
	// Amount is intentionally unknown for PayPear (possible commission) → fail-open.
	gotID, res, err := p.Webhook(context.Background(), []byte(`{"object":{"id":"pp-1","status":"CONFIRMED"}}`), http.Header{})
	if err != nil || gotID != "pp-1" || res.Status != StatusPaid || res.AmountKopecks != 0 {
		t.Fatalf("webhook: id=%q res=%+v err=%v", gotID, res, err)
	}
}

func TestAuraPayCreateAndWebhookRefetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-ApiKey") != "k" || r.Header.Get("X-ShopId") != "shop" {
			t.Errorf("missing auth headers")
		}
		switch r.URL.Path {
		case "/invoice/create":
			_, _ = w.Write([]byte(`{"id":"inv-1","payment_data":{"url":"https://pay.aurapay/inv-1"}}`))
		case "/invoice/status":
			_, _ = w.Write([]byte(`{"status":"PAID","amount":150,"currency":"RUB"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	a := &AuraPay{apiKey: "k", shopID: "shop", base: srv.URL}
	id, url, err := a.Create(context.Background(), CreateReq{AmountRub: 150, OrderID: 5})
	if err != nil || id != "inv-1" || url != "https://pay.aurapay/inv-1" {
		t.Fatalf("create: id=%q url=%q err=%v", id, url, err)
	}
	gotID, res, err := a.Webhook(context.Background(), []byte(`{"id":"inv-1","status":"PAID"}`), http.Header{})
	if err != nil || gotID != "inv-1" || res.Status != StatusPaid || res.AmountKopecks != 15000 {
		t.Fatalf("webhook: id=%q res=%+v err=%v", gotID, res, err)
	}
}

func TestRollyPayStatusMapping(t *testing.T) {
	cases := map[string]Status{
		"paid": StatusPaid, "created": StatusPending, "processing": StatusPending,
		"expired": StatusCanceled, "canceled": StatusCanceled, "chargeback": StatusCanceled,
	}
	for api, want := range cases {
		got := rollyPayPayment{Status: api, Amount: "10.00"}.result()
		if got.Status != want {
			t.Errorf("rollyPay status %q → %q, want %q", api, got.Status, want)
		}
	}
}
