package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/payments"
	"github.com/AppsGanin/rospanel/internal/plugin"
)

// A payment method a plugin provides goes through everything a built-in provider
// does — the order, the provider's page, the signed callback on the public route,
// the panel's own check of the amount — and the plan is granted only for a payment
// that matches the order.
//
// Not parallel: it registers the plugin's provider in the global payment registry.
func TestPluginPaymentEndToEnd(t *testing.T) {
	rt, st := rolesTestRouter(t)
	if err := st.SetTLS("vpn.example.com", "vpn.example.com", "self", "", ""); err != nil {
		t.Fatal(err)
	}
	host := plugin.New(plugin.Deps{Store: st, DataDir: rt.dataDir, PanelVersion: "4.4.0",
		Logger: slog.New(slog.DiscardHandler), PublicURL: rt.mgr.PaymentWebhookURL})
	rt.SetPlugins(host)
	payments.SetExtra(host.PaymentDescriptors)
	t.Cleanup(func() {
		payments.SetExtra(func() []payments.Descriptor { return nil })
		_ = host.Close(context.Background())
	})

	raw := pluginZip(t, map[string]string{
		"plugin.json": `{"id": "paytest", "version": "1.0.0", "api": 1, "name": "Pay test", "permissions": ["payments.manage"],
			"settings": [{"key": "secret", "kind": "secret", "label": "Secret"}],
			"provides": {"payment": {"label": "PayTest"}, "http": true}}`,
		"main.js": `
export const payment = {
  create(req) {
    return { provider_id: "inv-" + req.order_id, pay_url: "https://pay.example/inv-" + req.order_id + "?hook=" + encodeURIComponent(req.webhook_url) };
  },
  status(id) { return { status: "pending" }; },
  webhook({ body, headers }) {
    if (headers["x-sign"] !== panel.crypto.hmac("sha256", panel.config.secret, body)) throw new Error("bad signature");
    const b = JSON.parse(body);
    return { provider_id: b.id, status: b.status, amount_kopecks: b.rub * 100, currency: "RUB" };
  },
};
export function onHttp(req) {
  return { status: 201, headers: { "Content-Type": "text/html", "Set-Cookie": "rcsid=stolen" },
    body: "<script>steal()</script>" + req.method + " " + req.path + " " + (req.headers.cookie || "no-cookie") };
}`,
	})
	ctx := context.Background()
	pkg, err := host.Inspect(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Install(ctx, raw, plugin.Consent{SHA256: pkg.SHA256, Perms: pkg.Manifest.Permissions}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.SetConfig(ctx, "paytest", map[string]string{"secret": "k3y"}); err != nil {
		t.Fatal(err)
	}
	info, err := host.Enable(ctx, "paytest")
	if err != nil {
		t.Fatal(err)
	}
	rt.ensurePluginCallbacks(info)
	secret := rt.mgr.PaymentWebhookSecret()
	if secret == "" || !strings.HasSuffix(info.PaymentWebhook, "/"+secret+"/plugin.paytest") || !strings.HasSuffix(info.HTTPURL, "/x/paytest") {
		t.Fatalf("callback URLs: %+v (secret %q)", info, secret)
	}

	// The operator switches the method on like any provider; it needs no fields.
	const key = "plugin.paytest"
	if err := rt.mgr.SavePaymentProvider(key, true, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rt.mgr.PaymentMethods(), ","); !strings.Contains(got, key) {
		t.Fatalf("payment methods: %s", got)
	}

	u, _ := st.CreateUser("buyer", "uuid-buyer", "pw", "tok-buyer", 0, 0, 0)
	plan := &model.TariffPlan{Slug: "m1", Name: "Month", PriceRub: 150, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	order, err := rt.mgr.StartPlanPayment(ctx, i18n.RU, u.ID, plan.ID, key, 1)
	if err != nil {
		t.Fatal(err)
	}
	if order.ProviderID != fmt.Sprintf("inv-%d", order.ID) || !strings.HasPrefix(order.PayURL, "https://pay.example/") ||
		!strings.Contains(order.PayURL, "plugin.paytest") {
		t.Fatalf("order: %+v", order)
	}

	callback := func(body, sig string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/"+secret+"/"+key, strings.NewReader(body))
		req.Header.Set("X-Sign", sig)
		rec := httptest.NewRecorder()
		handlePaymentWebhook(rt, rec, req, key)
		if rec.Code != http.StatusOK {
			t.Fatalf("callback answered %d", rec.Code)
		}
	}
	sign := func(body string) string {
		mac := hmac.New(sha256.New, []byte("k3y"))
		mac.Write([]byte(body))
		return hex.EncodeToString(mac.Sum(nil))
	}
	statusOf := func() string {
		o, err := st.GetPaymentOrder(order.ID)
		if err != nil {
			t.Fatal(err)
		}
		return o.Status
	}
	paid := fmt.Sprintf(`{"id":"inv-%d","status":"paid","rub":150}`, order.ID)
	cheap := fmt.Sprintf(`{"id":"inv-%d","status":"paid","rub":1}`, order.ID)

	callback(paid, "forged")
	if s := statusOf(); s != "pending" {
		t.Fatalf("a forged callback moved the order to %s", s)
	}
	callback(cheap, sign(cheap))
	if s := statusOf(); s != "pending" {
		t.Fatalf("an underpaid callback moved the order to %s", s)
	}
	callback(paid, sign(paid))
	if s := statusOf(); s != "paid" {
		t.Fatalf("a valid callback left the order %s", s)
	}
	got, _ := st.GetUser(u.ID)
	if got.PlanID != plan.ID || got.ExpireAt == 0 {
		t.Fatalf("the plan was not granted: %+v", got)
	}

	// onHttp: the plugin's answer cannot run script with the panel's origin or
	// set its cookies, and the panel's own cookie never reaches the plugin.
	req := httptest.NewRequest(http.MethodPost, "/"+secret+"/x/paytest/hook?a=1", strings.NewReader("{}"))
	req.Header.Set("Cookie", "rcsid=admin-session")
	rec := httptest.NewRecorder()
	rt.handlePluginHTTP(rec, req, "/paytest/hook", http.NotFoundHandler())
	if rec.Code != http.StatusCreated || rec.Body.String() != "<script>steal()</script>POST /hook no-cookie" {
		t.Fatalf("onHttp: %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") != "sandbox" || rec.Header().Get("Set-Cookie") != "" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("onHttp headers: %v", rec.Header())
	}
	rec = httptest.NewRecorder()
	rt.handlePluginHTTP(rec, httptest.NewRequest(http.MethodGet, "/x/nope/", nil), "/nope/", http.NotFoundHandler())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown plugin was not the decoy: %d", rec.Code)
	}

	// Switched off, the method is gone from the registry.
	if _, err := host.Disable(ctx, "paytest"); err != nil {
		t.Fatal(err)
	}
	if _, ok := payments.Get(key); ok {
		t.Fatal("a disabled plugin's payment method is still registered")
	}
}
