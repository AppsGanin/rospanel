package payments

import (
	"context"
	"crypto/sha1" //nolint:gosec // the provider's algorithm
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func yooMoneyNote(secret string, f url.Values) string {
	signed := strings.Join([]string{f.Get("notification_type"), f.Get("operation_id"), f.Get("amount"),
		f.Get("currency"), f.Get("datetime"), f.Get("sender"), f.Get("codepro"), secret, f.Get("label")}, "&")
	sum := sha1.Sum([]byte(signed)) //nolint:gosec // the provider's algorithm
	f.Set("sha1_hash", hex.EncodeToString(sum[:]))
	return f.Encode()
}

func TestYooMoneyWebhook(t *testing.T) {
	t.Parallel()
	y := &YooMoney{wallet: "4100111", secret: "s3cret"}
	f := url.Values{
		"notification_type": {"card-incoming"}, "operation_id": {"904035776918098009"},
		"amount": {"97.00"}, "withdraw_amount": {"100.00"}, "currency": {"643"},
		"datetime": {"2026-09-29T10:00:00Z"}, "sender": {""}, "codepro": {"false"}, "label": {"rp7-aa"},
	}
	id, res, err := y.Webhook(context.Background(), []byte(yooMoneyNote("s3cret", f)), nil)
	if err != nil || id != "rp7-aa" || res.Status != StatusPaid || res.AmountKopecks != 10000 || res.Currency != "RUB" {
		t.Fatalf("webhook = %q %+v %v", id, res, err)
	}
	if _, _, err := y.Webhook(context.Background(), []byte(yooMoneyNote("other", f)), nil); err == nil {
		t.Fatal("a notification signed with another secret was accepted")
	}
	// No charged amount, or one the signed amount contradicts: refused, not trusted.
	g := url.Values{}
	for k, v := range f {
		g[k] = v
	}
	g.Del("withdraw_amount")
	if _, _, err := y.Webhook(context.Background(), []byte(yooMoneyNote("s3cret", g)), nil); err == nil {
		t.Fatal("a notification with no withdraw_amount was accepted")
	}
	g.Set("withdraw_amount", "5000.00")
	if _, _, err := y.Webhook(context.Background(), []byte(yooMoneyNote("s3cret", g)), nil); err == nil {
		t.Fatal("a withdraw_amount far above the signed amount was accepted")
	}
	f.Set("codepro", "true")
	if _, res, err := y.Webhook(context.Background(), []byte(yooMoneyNote("s3cret", f)), nil); err != nil || res.Status != StatusPending {
		t.Fatalf("protected transfer = %+v %v", res, err)
	}
}

func TestYooMoneyCreateFollowsRedirect(t *testing.T) {
	t.Parallel()
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		http.Redirect(w, r, "/transfer/quickpay?requestId=abc", http.StatusFound)
	}))
	defer srv.Close()
	y := &YooMoney{wallet: "4100111", secret: "s", base: srv.URL + "/quickpay/confirm"}
	label, link, err := y.Create(context.Background(), CreateReq{AmountRub: 150, OrderID: 9, Description: "Месяц"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(label, "rp9-") || link != srv.URL+"/transfer/quickpay?requestId=abc" {
		t.Fatalf("label %q link %q", label, link)
	}
	if form.Get("sum") != "150" || form.Get("receiver") != "4100111" || form.Get("label") != label || form.Get("paymentType") != "AC" {
		t.Fatalf("form = %v", form)
	}
}

func TestStarsInvoice(t *testing.T) {
	t.Parallel()
	if n, err := StarsFor(100, "1.8"); err != nil || n != 56 { // 100 / 1.8 = 55.6 → 56
		t.Fatalf("StarsFor = %d %v", n, err)
	}
	if _, err := StarsFor(100, "0"); err == nil {
		t.Fatal("a zero rate was accepted")
	}
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/botTOKEN/createInvoiceLink") {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(`{"ok":true,"result":"https://t.me/$abc"}`))
	}))
	defer srv.Close()
	s := &Stars{token: "TOKEN", rate: "2", base: srv.URL + "/bot"}
	id, link, err := s.Create(context.Background(), CreateReq{AmountRub: 199, OrderID: 12, Description: "Тариф «Месяц»"})
	if err != nil || link != "https://t.me/$abc" || id != "o12-100" {
		t.Fatalf("create = %q %q %v", id, link, err)
	}
	if sent["currency"] != "XTR" || sent["payload"] != "o12-100" {
		t.Fatalf("invoice = %v", sent)
	}
	if n, ok := StarsPayloadAmount(id); !ok || n != 100 {
		t.Fatalf("payload amount = %d %v", n, ok)
	}
	if _, _, err := s.Webhook(context.Background(), []byte(`{}`), nil); err == nil {
		t.Fatal("a posted Stars callback was accepted")
	}
}
