package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// What an outside bot needs to sell on its own: the prices the user sees (balance
// included), a top-up order, the user's own orders, their renewal switch and promo
// codes.
func TestAPIWalletForBots(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	plan := &model.TariffPlan{Slug: "bot-std", Name: "Std", PriceRub: 199, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	set.BillingEnabled, set.BillingManualEnabled, set.WalletEnabled, set.WalletTopupMin = true, true, true, 10
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	u, err := mgr.CreateUser(t.Context(), "botbuyer", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := mgr.CreateUser(t.Context(), "bystander", 0, 0)
	id := itoa64(u.ID)

	var created struct {
		Data struct {
			Order model.PaymentOrder `json:"order"`
		} `json:"data"`
	}
	rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", key,
		`{"user_id":`+id+`,"kind":"topup","amount_rub":150}`)
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil ||
		created.Data.Order.Kind != model.OrderTopup || created.Data.Order.AmountRub != 150 {
		t.Fatalf("top-up: %d %s", rec.Code, rec.Body.String())
	}
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", key,
		`{"user_id":`+id+`,"kind":"gift"}`); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind: %d %s", rec.Code, rec.Body.String())
	}
	if err := mgr.ConfirmPayment(t.Context(), created.Data.Order.ID); err != nil {
		t.Fatal(err)
	}

	var quotes struct {
		Data []struct {
			PlanID int64 `json:"plan_id"`
			Quotes []struct {
				TotalRub   int   `json:"total_rub"`
				BalanceKop int64 `json:"balance_kop"`
				MoneyRub   int   `json:"money_rub"`
			} `json:"quotes"`
		} `json:"data"`
	}
	rec = apiGet(t, h, base+"/v1/users/"+id+"/quotes", key)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &quotes) != nil || len(quotes.Data) == 0 {
		t.Fatalf("quotes: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range quotes.Data {
		if p.PlanID == plan.ID {
			if q := p.Quotes[0]; q.TotalRub != 199 || q.BalanceKop != 15000 || q.MoneyRub != 49 {
				t.Fatalf("quote = %+v, want 150 ₽ of 199 from the balance", q)
			}
		}
	}

	var list struct {
		Data []model.PaymentOrder `json:"data"`
	}
	rec = apiGet(t, h, base+"/v1/billing/orders?user_id="+itoa64(other.ID), key)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Data) != 0 {
		t.Fatalf("another user's orders: %d %s", rec.Code, rec.Body.String())
	}

	rec = apiDo(t, h, http.MethodPost, base+"/v1/users/"+id+"/autorenew", key, `{"on":false}`)
	if w, _ := st.GetWallet(u.ID); rec.Code != http.StatusOK || w.AutoRenew {
		t.Fatalf("autorenew off: %d %s", rec.Code, rec.Body.String())
	}
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/users/"+id+"/promo", key, `{"code":"NOPE"}`); rec.Code == http.StatusOK {
		t.Fatalf("an unknown code was accepted: %s", rec.Body.String())
	}
}

// An outside bot finds the user writing to it, links their Telegram, and records who
// invited them — once, and never a chat or a referrer that belongs elsewhere.
func TestAPIBotIdentity(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	set, _ := st.GetSettings()
	set.BillingEnabled, set.RefMode, set.RefPercent, set.WalletEnabled, set.WalletTopupMin = true, model.RefPercent, 10, true, 10
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	ref, _ := mgr.CreateUser(t.Context(), "inviter", 0, 0)
	u, _ := mgr.CreateUser(t.Context(), "newcomer", 0, 0)
	other, _ := mgr.CreateUser(t.Context(), "someone", 0, 0)
	id := itoa64(u.ID)

	if rec := apiDo(t, h, http.MethodPost, base+"/v1/users/"+id+"/telegram", key, `{"chat_id":555123}`); rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	var found struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	rec := apiGet(t, h, base+"/v1/users?telegram_id=555123", key)
	if json.Unmarshal(rec.Body.Bytes(), &found) != nil || len(found.Data) != 1 || found.Data[0].ID != u.ID {
		t.Fatalf("lookup by telegram: %s", rec.Body.String())
	}
	rec = apiGet(t, h, base+"/v1/users?sub_token="+u.SubToken, key)
	if json.Unmarshal(rec.Body.Bytes(), &found) != nil || len(found.Data) != 1 || found.Data[0].ID != u.ID {
		t.Fatalf("lookup by token: %s", rec.Body.String())
	}
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/users/"+itoa64(other.ID)+"/telegram", key, `{"chat_id":555123}`); rec.Code == http.StatusOK {
		t.Fatal("a chat linked to one user was moved to another")
	}

	code, err := mgr.RefCode(ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/users/"+id+"/referrer", key, `{"ref_code":"r_`+code+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("referrer by code: %d %s", rec.Code, rec.Body.String())
	}
	if w, _ := st.GetWallet(u.ID); w.ReferrerID != ref.ID {
		t.Fatalf("referrer = %d", w.ReferrerID)
	}
	for _, body := range []string{`{"referrer_id":` + itoa64(other.ID) + `}`, `{"ref_code":"nope"}`} {
		if rec := apiDo(t, h, http.MethodPost, base+"/v1/users/"+id+"/referrer", key, body); rec.Code == http.StatusOK {
			t.Fatalf("%s replaced the referrer", body)
		}
	}
	// The inviter cannot then be recorded as invited by the one they invited.
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/users/"+itoa64(ref.ID)+"/referrer", key,
		`{"referrer_id":`+id+`}`); rec.Code == http.StatusOK {
		t.Fatal("a referral loop was accepted")
	}
}

// The subscription page as data: enough to draw it — the link, the app imports, the
// figures in words in the language asked for.
func TestAPISubscriptionView(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	u, err := mgr.CreateUser(t.Context(), "viewer", 10<<30, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data struct {
			Name        string `json:"name"`
			StatusLabel string `json:"status_label"`
			LimitBytes  int64  `json:"limit_bytes"`
			SubURL      string `json:"sub_url"`
			Apps        []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"apps"`
			Texts struct {
				Limit string `json:"limit"`
			} `json:"texts"`
		} `json:"data"`
	}
	rec := apiGet(t, h, base+"/v1/users/"+itoa64(u.ID)+"/subscription?lang=ru", key)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("view: %d %s", rec.Code, rec.Body.String())
	}
	d := got.Data
	if d.Name != "viewer" || d.LimitBytes != 10<<30 || d.SubURL == "" || len(d.Apps) == 0 ||
		d.Apps[0].URL == "" || d.Texts.Limit == "" || d.StatusLabel != "Активно" {
		t.Fatalf("view = %+v", d)
	}
}

// A bot's key holds billing.sell: it opens orders but cannot mark one paid or credit a
// balance. A body that leaves out the one field that matters is refused, not read as
// "unlink" or "off".
func TestAPISellingKeyAndRequiredFields(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, _ := apiFixture(t, h, st)
	plan := &model.TariffPlan{Slug: "sell-std", Name: "Std", PriceRub: 199, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	set.BillingEnabled, set.BillingManualEnabled = true, true
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	role, err := st.CreateAdminRole("seller", []string{model.PermUsersManage, model.PermBillingSell})
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.CreateAPIKey("bot", role.Key)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := mgr.CreateUser(t.Context(), "buyer", 0, 0)
	id := itoa64(u.ID)
	var created struct {
		Data struct {
			Order model.PaymentOrder `json:"order"`
		} `json:"data"`
	}
	rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", k.RawKey,
		`{"user_id":`+id+`,"plan_id":`+itoa64(plan.ID)+`,"lang":"ru"}`)
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil {
		t.Fatalf("a seller opens an order: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range []struct{ path, body string }{
		{"/v1/billing/orders/" + itoa64(created.Data.Order.ID) + "/confirm", `{}`},
		{"/v1/users/" + id + "/balance", `{"amount_kop":100000}`},
	} {
		if rec := apiDo(t, h, http.MethodPost, base+c.path, k.RawKey, c.body); rec.Code != http.StatusForbidden {
			t.Fatalf("a seller reached %s: %d", c.path, rec.Code)
		}
	}
	_ = st.SetUserTelegramChat(u.ID, 42424242)
	for _, path := range []string{"/v1/users/" + id + "/telegram", "/v1/users/" + id + "/autorenew"} {
		if rec := apiDo(t, h, http.MethodPost, base+path, k.RawKey, `{}`); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s with an empty body: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if got, _ := st.GetUser(u.ID); got.TgChatID != 42424242 {
		t.Fatal("an empty body unlinked the Telegram")
	}
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", k.RawKey,
		`{"user_id":`+id+`,"kind":"topup","amount_rub":100,"return_url":"javascript:alert(1)"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a script return_url: %d", rec.Code)
	}
}
