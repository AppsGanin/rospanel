package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Money an external system takes itself: an order with provider "external", whatever
// the manual-payment setting, paid in the same call or confirmed later — and paid only
// for a key that may confirm.
func TestAPIExternalOrder(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	plan := &model.TariffPlan{Slug: "ext-std", Name: "Std", PriceRub: 199, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	set.BillingEnabled, set.BillingManualEnabled, set.WalletEnabled, set.WalletTopupMin = true, false, true, 10
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	u, err := mgr.CreateUser(t.Context(), "starbuyer", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	id := itoa64(u.ID)
	order := func(rec *http.Response, body []byte) model.PaymentOrder {
		t.Helper()
		var out struct {
			Data struct {
				Order model.PaymentOrder `json:"order"`
			} `json:"data"`
		}
		if rec.StatusCode != http.StatusCreated || json.Unmarshal(body, &out) != nil {
			t.Fatalf("order: %d %s", rec.StatusCode, body)
		}
		return out.Data.Order
	}

	rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", key,
		`{"user_id":`+id+`,"plan_id":`+itoa64(plan.ID)+`,"provider":"external","paid":true,"external_ref":"stars-1"}`)
	o := order(rec.Result(), rec.Body.Bytes())
	if o.Status != "paid" || o.Provider != model.ExternalPayProvider || o.ProviderID != "stars-1" {
		t.Fatalf("paid at once: %+v", o)
	}
	if got, _ := st.GetUser(u.ID); got.PlanID != plan.ID {
		t.Fatalf("the plan was not applied: %d", got.PlanID)
	}

	rec = apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", key,
		`{"user_id":`+id+`,"kind":"topup","amount_rub":50,"provider":"external"}`)
	o = order(rec.Result(), rec.Body.Bytes())
	if o.Status != "pending" {
		t.Fatalf("two-step top-up: %+v", o)
	}
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/orders/"+itoa64(o.ID)+"/confirm", key, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}
	if w, _ := st.GetWalletLite(u.ID); w.BalanceKop != 5000 {
		t.Fatalf("balance = %d, want 5000", w.BalanceKop)
	}

	if rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", key,
		`{"user_id":`+id+`,"plan_id":`+itoa64(plan.ID)+`,"paid":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("paid without provider external: %d %s", rec.Code, rec.Body.String())
	}

	// A key that may open orders but not confirm them.
	k, err := st.CreateAPIKey("seller", false, nil, []string{"POST /v1/billing/orders"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", k.RawKey,
		`{"user_id":`+id+`,"plan_id":`+itoa64(plan.ID)+`,"provider":"external","paid":true}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a seller confirmed its own order: %d %s", rec.Code, rec.Body.String())
	}
	rec = apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", k.RawKey,
		`{"user_id":`+id+`,"plan_id":`+itoa64(plan.ID)+`,"provider":"external"}`)
	if o := order(rec.Result(), rec.Body.Bytes()); o.Status != "pending" {
		t.Fatalf("a seller's external order: %+v", o)
	}
}
