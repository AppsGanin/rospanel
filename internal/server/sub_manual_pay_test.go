package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// fetchSubHTML asks for the human page the way a browser does.
func fetchSubHTML(h http.Handler, token string) string {
	req := httptest.NewRequest(http.MethodGet, "/sub/"+token, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Body.String()
}

// postPay asks the subscription's pay route for one plan through one method, the way
// the page's own button does.
func postPay(h http.Handler, token string, planID int64, provider string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"plan_id": planID, "provider": provider})
	req := httptest.NewRequest(http.MethodPost, "/sub/"+token+"/pay", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-RosPanel-Sub", "1")
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Manual payment is a method the operator switches on, not a fallback for having no
// provider: with it off the page offers nothing to pay with and the route refuses,
// with it on the page names it and a tap opens a pending order.
func TestManualPaymentIsOfferedOnlyWhenItIsOn(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "payer", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan := &model.TariffPlan{Name: "Paid", Slug: "paid", PriceRub: 300, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ApplyPlanToUser(t.Context(), u.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	set.BillingEnabled = true
	set.BillingPaymentNote = "Transfer to card 0000"
	set.BillingManualEnabled = false
	if err := mgr.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}

	page := fetchSubHTML(h, u.SubToken)
	if strings.Contains(page, `payWith('`+sub.ManualPayKey) || strings.Contains(page, `"`+sub.ManualPayKey+`"`) {
		t.Error("the page offers manual payment while it is switched off")
	}
	// The plan is still named as the user's current one; what must be gone is the
	// button, since nothing would answer it.
	if strings.Contains(page, `onclick="pay(`) {
		t.Error("the page offers a pay button with no payment method behind it")
	}
	if rec := postPay(h, u.SubToken, plan.ID, sub.ManualPayKey); rec.Code == http.StatusOK {
		t.Errorf("manual order opened while manual payment is off: %s", rec.Body.String())
	}

	// Switched on: the page names the method and the route opens the order.
	set, _ = st.GetSettings()
	set.BillingManualEnabled = true
	if err := mgr.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	page = fetchSubHTML(h, u.SubToken)
	for _, want := range []string{`onclick="pay(`, sub.ManualPayKey, set.BillingPaymentNote} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q once manual payment is on", want)
		}
	}
	rec := postPay(h, u.SubToken, plan.ID, sub.ManualPayKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("pay: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Manual  bool   `json:"manual"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !got.Manual {
		t.Fatalf("answer: %s (%v)", rec.Body.String(), err)
	}
	if !strings.Contains(got.Message, set.BillingPaymentNote) {
		t.Errorf("the instructions carry no payment details: %q", got.Message)
	}
	orders, err := st.ListPaymentOrders("", 10)
	if err != nil || len(orders) != 1 || orders[0].Provider != "" {
		t.Fatalf("orders = %+v (%v)", orders, err)
	}
}

// Manual payment stands beside the providers rather than instead of them: with a
// provider enabled as well, the page offers both and manual comes first.
func TestManualPaymentStandsBesideAProvider(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "payer", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan := &model.TariffPlan{Name: "Paid", Slug: "paid", PriceRub: 300, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ApplyPlanToUser(t.Context(), u.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	set.BillingEnabled = true
	set.BillingManualEnabled = true
	set.BillingPaymentNote = "Transfer to card 0000"
	// The operator's own wording for the method, the way a provider carries a display
	// name. It shows where the methods are named: the choice a second method brings.
	set.BillingManualLabel = "Перевод на карту"
	if err := mgr.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SavePaymentProvider("cryptobot", true, map[string]string{"token": "1:aaaaaaaa"}); err != nil {
		t.Fatal(err)
	}

	page := fetchSubHTML(h, u.SubToken)
	manual, provider := strings.Index(page, `"`+sub.ManualPayKey+`"`), strings.Index(page, `"cryptobot"`)
	if manual < 0 || provider < 0 {
		t.Fatalf("page offers manual at %d and the provider at %d", manual, provider)
	}
	if manual > provider {
		t.Error("the provider is offered before manual payment")
	}
	if !strings.Contains(page, set.BillingManualLabel) {
		t.Error("the method choice does not carry the operator's label for manual payment")
	}
	// The details belong to the manual order the user opens, not to a card someone
	// paying through the provider is reading.
	if strings.Contains(page, set.BillingPaymentNote) {
		t.Error("the card shows the manual details although a provider is offered too")
	}
	// Manual still opens a manual order while a provider is on — the two do not
	// shadow each other. (The provider's own checkout is not exercised here: it
	// would call the provider's API.)
	if rec := postPay(h, u.SubToken, plan.ID, sub.ManualPayKey); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"manual":true`) {
		t.Errorf("manual payment beside a provider: %d %s", rec.Code, rec.Body.String())
	}
}
