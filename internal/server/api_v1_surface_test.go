package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// The endpoints an integration needs but the panel used to keep to itself. Exercised
// through the real /v1 mux against a real store, because the point of each is that it
// is REACHABLE with a key — a handler that works but isn't wired (or is wired to the
// wrong verb) is the exact failure this suite exists to catch.

func apiTestRouter(t *testing.T) (*Router, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	mgr := core.New(st, sup, xray.Options{PanelDest: "127.0.0.1:8080"}, core.TLSPaths{}, dir)
	return &Router{mgr: mgr, dataDir: dir}, st
}

// apiCall runs one request through the authenticated mux (auth itself is covered
// elsewhere, so this drives apiMux directly) and returns the status and the decoded
// `data` payload.
func apiCall(t *testing.T, rt *Router, method, path, body string) (int, json.RawMessage) {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rt.apiMux().ServeHTTP(w, req)

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env.Data
}

// Webhooks are the push half of an integration: an endpoint added over the API must
// be listed, updated, tested and deleted over the same API.
func TestAPIWebhooksRoundTrip(t *testing.T) {
	rt, _ := apiTestRouter(t)

	code, data := apiCall(t, rt, http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/hook","events":["user.created"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create webhook: %d (%s)", code, data)
	}
	var created model.Webhook
	if err := json.Unmarshal(data, &created); err != nil || created.ID == 0 {
		t.Fatalf("create returned %s (%v)", data, err)
	}

	code, data = apiCall(t, rt, http.MethodGet, "/v1/webhooks", "")
	var list []model.Webhook
	if code != http.StatusOK || json.Unmarshal(data, &list) != nil || len(list) != 1 {
		t.Fatalf("list webhooks: %d %s", code, data)
	}

	// A bad event key must be refused rather than stored to never fire.
	if code, _ = apiCall(t, rt, http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/x","events":["nope.nope"]}`); code != http.StatusBadRequest {
		t.Fatalf("unknown event accepted: %d", code)
	}
	// As must a URL that isn't one.
	if code, _ = apiCall(t, rt, http.MethodPost, "/v1/webhooks",
		`{"url":"ftp://example.com","events":[]}`); code != http.StatusBadRequest {
		t.Fatalf("bad URL accepted: %d", code)
	}

	// The subscribable keys are published, or nobody outside the panel knows them.
	code, data = apiCall(t, rt, http.MethodGet, "/v1/webhooks/events", "")
	var keys []apiEventKey
	if code != http.StatusOK || json.Unmarshal(data, &keys) != nil || len(keys) == 0 {
		t.Fatalf("webhook event catalog: %d %s", code, data)
	}

	// Update: disabling must stick (the flag is a pointer precisely so it can).
	off := `{"url":"https://example.com/hook","events":["user.created"],"enabled":false}`
	if code, data = apiCall(t, rt, http.MethodPost, "/v1/webhooks/1", off); code != http.StatusOK {
		t.Fatalf("update webhook: %d %s", code, data)
	}
	_, data = apiCall(t, rt, http.MethodGet, "/v1/webhooks", "")
	_ = json.Unmarshal(data, &list)
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("disable did not stick: %s", data)
	}

	if code, data = apiCall(t, rt, http.MethodDelete, "/v1/webhooks/1", ""); code != http.StatusOK {
		t.Fatalf("delete webhook: %d %s", code, data)
	}
	_, data = apiCall(t, rt, http.MethodGet, "/v1/webhooks", "")
	_ = json.Unmarshal(data, &list)
	if len(list) != 0 {
		t.Fatalf("webhook survived delete: %s", data)
	}
}

// The journals: a change made over the API must be readable back over the API, both
// in the user's own trail and in the admin trail.
func TestAPIJournalsAreReadable(t *testing.T) {
	rt, _ := apiTestRouter(t)

	code, data := apiCall(t, rt, http.MethodPost, "/v1/users", `{"name":"journal-user"}`)
	if code != http.StatusCreated {
		t.Fatalf("create user: %d %s", code, data)
	}
	var u userView
	if err := json.Unmarshal(data, &u); err != nil {
		t.Fatalf("decode user: %v", err)
	}

	code, data = apiCall(t, rt, http.MethodGet, "/v1/events", "")
	var events apiEventsResp
	if code != http.StatusOK || json.Unmarshal(data, &events) != nil || len(events.Events) == 0 {
		t.Fatalf("global journal empty right after a create: %d %s", code, data)
	}
	code, data = apiCall(t, rt, http.MethodGet, "/v1/users/1/events", "")
	if code != http.StatusOK || json.Unmarshal(data, &events) != nil || len(events.Events) == 0 {
		t.Fatalf("user journal empty: %d %s", code, data)
	}
	if events.Events[0].UserID != u.ID {
		t.Fatalf("user journal returned someone else's row: %+v", events.Events[0])
	}

	// A typo'd filter must fail loudly — an empty page would read as "nothing happened".
	if code, _ = apiCall(t, rt, http.MethodGet, "/v1/events?action=user.nonsense", ""); code != http.StatusBadRequest {
		t.Fatalf("unknown action filter answered %d, want 400", code)
	}
	if code, _ = apiCall(t, rt, http.MethodGet, "/v1/admin-audit?category=nonsense", ""); code != http.StatusBadRequest {
		t.Fatalf("unknown category answered %d, want 400", code)
	}

	// Both vocabularies are published so the filters are usable from outside.
	if code, data = apiCall(t, rt, http.MethodGet, "/v1/events/catalog", ""); code != http.StatusOK ||
		!strings.Contains(string(data), `"key"`) {
		t.Fatalf("event catalog: %d %s", code, data)
	}
	code, data = apiCall(t, rt, http.MethodGet, "/v1/admin-audit/catalog", "")
	var cat apiAuditCatalogResp
	if code != http.StatusOK || json.Unmarshal(data, &cat) != nil ||
		len(cat.Categories) == 0 || len(cat.Actions) == 0 {
		t.Fatalf("audit catalog: %d %s", code, data)
	}
}

// Creating a user in one call: the device limit, the groups and the plan all land,
// and the response is the user as it actually ended up.
func TestAPICreateUserAppliesEverything(t *testing.T) {
	rt, st := apiTestRouter(t)

	g, err := st.CreateGroup("VIP", []string{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS)})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	plan := &model.TariffPlan{Slug: "one-month", Name: "Month", PriceRub: 100, PeriodDays: 30, DataLimit: 5 << 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}

	code, data := apiCall(t, rt, http.MethodPost, "/v1/users",
		`{"name":"full","device_limit":3,"group_ids":[`+id64(g.ID)+`],"plan_id":`+id64(plan.ID)+`}`)
	if code != http.StatusCreated {
		t.Fatalf("create user: %d %s", code, data)
	}
	var u userView
	if err := json.Unmarshal(data, &u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.DeviceLimit != 3 {
		t.Errorf("device_limit = %d, want 3", u.DeviceLimit)
	}
	if u.PlanID != plan.ID {
		t.Errorf("plan_id = %d, want %d", u.PlanID, plan.ID)
	}
	if u.DataLimit != plan.DataLimit {
		t.Errorf("data_limit = %d, want the plan's %d", u.DataLimit, plan.DataLimit)
	}
	if len(u.Groups) != 1 || u.Groups[0].ID != g.ID {
		t.Errorf("groups = %+v, want just %q", u.Groups, g.Name)
	}
	// Gated by that group, the response must show only the granted lane.
	for _, l := range u.Links {
		if strings.Contains(l.Name, "HYSTERIA") {
			t.Errorf("a lane the group does not grant leaked into the response: %+v", u.Links)
		}
	}
}

// Cancelling a subscription over the API is its own operation, not "apply the free
// plan": it must move the user AND record the cancellation.
func TestAPICancelSubscription(t *testing.T) {
	rt, st := apiTestRouter(t)

	free := &model.TariffPlan{Slug: "cancel-free", Name: "Free tier", PriceRub: 0, PeriodDays: 30, Enabled: true}
	paid := &model.TariffPlan{Slug: "cancel-paid", Name: "Paid tier", PriceRub: 500, PeriodDays: 30, Enabled: true}
	for _, p := range []*model.TariffPlan{free, paid} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save plan: %v", err)
		}
	}
	set, _ := st.GetSettings()
	set.BillingEnabled, set.BillingFreePlanID = true, free.ID
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatalf("billing settings: %v", err)
	}
	if code, data := apiCall(t, rt, http.MethodPost, "/v1/users", `{"name":"subscriber"}`); code != http.StatusCreated {
		t.Fatalf("create user: %d %s", code, data)
	}
	if code, data := apiCall(t, rt, http.MethodPost, "/v1/users/1/plan",
		`{"plan_id":`+id64(paid.ID)+`}`); code != http.StatusOK {
		t.Fatalf("apply plan: %d %s", code, data)
	}

	code, data := apiCall(t, rt, http.MethodPost, "/v1/users/1/plan/cancel", "")
	if code != http.StatusOK {
		t.Fatalf("cancel: %d %s", code, data)
	}
	var u userView
	if err := json.Unmarshal(data, &u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.PlanID != free.ID {
		t.Fatalf("plan after cancel = %d, want the free plan %d", u.PlanID, free.ID)
	}
	// The distinguishing part: it is recorded as a cancellation, which is what a
	// billing integration keys off. Applying the free plan by hand would not be.
	_, data = apiCall(t, rt, http.MethodGet, "/v1/events?action="+model.EventPlanCancelled, "")
	var events apiEventsResp
	if json.Unmarshal(data, &events) != nil || len(events.Events) == 0 {
		t.Fatalf("cancellation was not journalled: %s", data)
	}

	// A user who isn't there is a 404, not a 500.
	if code, _ = apiCall(t, rt, http.MethodPost, "/v1/users/999/plan/cancel", ""); code != http.StatusNotFound {
		t.Fatalf("cancel for a missing user: %d, want 404", code)
	}
}

// Billing configuration round-trips, and a plan's users can be moved off it — the
// only way to empty a plan before deleting it.
func TestAPIBillingSettingsAndMigration(t *testing.T) {
	rt, st := apiTestRouter(t)

	a := &model.TariffPlan{Slug: "plan-a", Name: "A", PriceRub: 100, PeriodDays: 30, Enabled: true}
	b := &model.TariffPlan{Slug: "plan-b", Name: "B", PriceRub: 200, PeriodDays: 30, Enabled: true}
	for _, p := range []*model.TariffPlan{a, b} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save plan: %v", err)
		}
	}

	body := `{"enabled":true,"free_plan_id":0,"trial_plan_id":0,"payment_note":"card 1234"}`
	code, data := apiCall(t, rt, http.MethodPost, "/v1/billing/settings", body)
	if code != http.StatusOK {
		t.Fatalf("save billing settings: %d %s", code, data)
	}
	code, data = apiCall(t, rt, http.MethodGet, "/v1/billing/settings", "")
	var got apiBillingSettingsReq
	if code != http.StatusOK || json.Unmarshal(data, &got) != nil {
		t.Fatalf("read billing settings: %d %s", code, data)
	}
	if !got.Enabled || got.PaymentNote != "card 1234" {
		t.Fatalf("settings did not round-trip: %+v", got)
	}

	// Two users on plan A, moved to B in one call.
	for _, n := range []string{"one", "two"} {
		if code, data = apiCall(t, rt, http.MethodPost, "/v1/users",
			`{"name":"`+n+`","plan_id":`+id64(a.ID)+`}`); code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", n, code, data)
		}
	}
	code, data = apiCall(t, rt, http.MethodPost, "/v1/billing/plans/"+id64(a.ID)+"/migrate",
		`{"to_plan_id":`+id64(b.ID)+`}`)
	var mig apiMigratedResp
	if code != http.StatusOK || json.Unmarshal(data, &mig) != nil || mig.Migrated != 2 {
		t.Fatalf("migrate: %d %s", code, data)
	}
	if n, _ := st.CountUsersOnPlan(a.ID); n != 0 {
		t.Fatalf("%d users still on the retired plan", n)
	}
}

// The panel's errors are raised as a code plus a Russian fallback and translated in
// the browser. The API has no browser, so it must do that itself — otherwise an
// integration in any language gets Russian prose, and the code (the one part worth
// branching on) is flattened to a generic "bad_request" on the way out.
func TestAPIErrorsAreTranslated(t *testing.T) {
	rt, st := apiTestRouter(t)

	code, body := apiErr(t, rt, http.MethodPost, "/v1/billing/plans", `{"name":"   ","price_rub":100}`)
	if code != http.StatusBadRequest {
		t.Fatalf("nameless plan: %d %+v", code, body)
	}
	if body.Key != "err.planNameRequired" {
		t.Errorf("key = %q, want the specific reason err.planNameRequired", body.Key)
	}
	if body.Code != "bad_request" {
		t.Errorf("code = %q, want the coarse bad_request every client already switches on", body.Code)
	}
	if hasCyrillic(body.Message) {
		t.Errorf("message is not English: %q", body.Message)
	}

	// A parameterised message must arrive filled in, with the parameters kept
	// alongside so a caller can re-render it in its own language.
	plan := &model.TariffPlan{Slug: "busy", Name: "Busy", PriceRub: 100, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if c, d := apiCall(t, rt, http.MethodPost, "/v1/users",
		`{"name":"on-the-plan","plan_id":`+id64(plan.ID)+`}`); c != http.StatusCreated {
		t.Fatalf("create user: %d %s", c, d)
	}
	code, body = apiErr(t, rt, http.MethodDelete, "/v1/billing/plans/"+id64(plan.ID), "")
	if code != http.StatusBadRequest || body.Key != "err.planHasUsers" {
		t.Fatalf("deleting a plan with users: %d %+v", code, body)
	}
	if hasCyrillic(body.Message) || !strings.Contains(body.Message, "1") {
		t.Errorf("message should be English and carry the count: %q", body.Message)
	}
	if body.Args["count"] == nil {
		t.Errorf("args lost the parameters: %+v", body.Args)
	}
}

// apiErrBody is the failure envelope, including the two fields the API adds for
// callers that localize themselves.
type apiErrBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Key     string         `json:"key"`
	Args    map[string]any `json:"args"`
}

func apiErr(t *testing.T, rt *Router, method, path, body string) (int, apiErrBody) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rt.apiMux().ServeHTTP(w, req)
	var env struct {
		Error apiErrBody `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env.Error
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if r >= 'А' && r <= 'я' {
			return true
		}
	}
	return false
}

// The backup manifest carries the panel's secret path for the restore side. An API
// key must not learn it: that path is what keeps the panel invisible to scanners, and
// an integration has no use for it.
func TestAPIBackupInfoHidesTheSecretPath(t *testing.T) {
	rt, _ := apiTestRouter(t)

	code, data := apiCall(t, rt, http.MethodGet, "/v1/backup/info", "")
	if code != http.StatusOK {
		t.Fatalf("backup info: %d %s", code, data)
	}
	var m backup.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.SecretPath != "" {
		t.Errorf("secret path leaked to the API: %q", m.SecretPath)
	}
	// The rest of the manifest is still there — redaction, not a blank response.
	if m.CreatedAt == "" {
		t.Errorf("manifest is empty: %+v", m)
	}
}

// The two vocabularies that make grants and inbounds constructible from outside.
func TestAPICatalogsArePublished(t *testing.T) {
	rt, _ := apiTestRouter(t)

	code, data := apiCall(t, rt, http.MethodGet, "/v1/groups/targets", "")
	var targets []core.GroupTarget
	if code != http.StatusOK || json.Unmarshal(data, &targets) != nil || len(targets) == 0 {
		t.Fatalf("group targets: %d %s", code, data)
	}
	// The master must be there with its built-in lanes and their ready-made tokens.
	if targets[0].ServerID != model.LocalNodeID || len(targets[0].Lanes) == 0 {
		t.Fatalf("unexpected first target: %+v", targets[0])
	}
	if targets[0].Lanes[0].Token != model.BuiltinToken(model.LocalNodeID, targets[0].Lanes[0].Lane) {
		t.Fatalf("lane token does not match the documented shape: %+v", targets[0].Lanes[0])
	}

	code, data = apiCall(t, rt, http.MethodGet, "/v1/inbounds/catalog", "")
	var cat inboundCatalogView
	if code != http.StatusOK || json.Unmarshal(data, &cat) != nil {
		t.Fatalf("inbound catalog: %d %s", code, data)
	}
	if len(cat.Protocols) == 0 || len(cat.Combos) == 0 || cat.Max == 0 || len(cat.Enums) == 0 {
		t.Fatalf("inbound catalog is missing parts: %+v", cat)
	}
}

// id64 keeps the request bodies above readable.
func id64(v int64) string { return strconv.FormatInt(v, 10) }

// PATCH /v1/settings is a PARTIAL update. The trap it has to avoid is the one the HWID
// group makes easy: those four fields are stored by a single call, so a naive handler
// that reads the request struct and writes all four would reset the limit and the TTL
// every time a caller flipped `hwid_enabled` alone — silently, and only visibly later
// when devices stopped being refused.
func TestAPISettingsPartialUpdateKeepsTheRest(t *testing.T) {
	rt, st := apiTestRouter(t)

	// Establish a known starting point through the store, the way an operator would
	// have set it up in the panel.
	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	set.HWIDEnabled, set.HWIDRequire = true, true
	set.HWIDFallbackLimit, set.HWIDTTLDays = 5, 14
	if err := st.SetHWIDSettings(set); err != nil {
		t.Fatalf("seed hwid: %v", err)
	}
	if err := rt.mgr.SetUserAutoDelete(30); err != nil {
		t.Fatalf("seed autodelete: %v", err)
	}

	// Touch exactly one HWID field and one unrelated one.
	code, data := apiCall(t, rt, "PATCH", "/v1/settings", `{"hwid_enabled":false}`)
	if code != http.StatusOK {
		t.Fatalf("PATCH /v1/settings = %d: %s", code, data)
	}
	var view apiSettingsView
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, data)
	}
	if view.HWIDEnabled {
		t.Error("hwid_enabled was not applied")
	}
	if view.HWIDFallbackLimit != 5 || view.HWIDTTLDays != 14 || !view.HWIDRequire {
		t.Errorf("the rest of the HWID group was reset: limit=%d ttl=%d require=%v, want 5/14/true",
			view.HWIDFallbackLimit, view.HWIDTTLDays, view.HWIDRequire)
	}
	if view.UserAutoDeleteDays != 30 {
		t.Errorf("an untouched setting changed: user_autodelete_days=%d, want 30", view.UserAutoDeleteDays)
	}

	// Negative values are refused rather than stored.
	if code, _ := apiCall(t, rt, "PATCH", "/v1/settings", `{"hwid_ttl_days":-1}`); code != http.StatusBadRequest {
		t.Errorf("negative ttl = %d, want 400", code)
	}

	// The panel's own secret path must never travel over /v1 — it is the obscurity
	// layer in front of the panel, not a setting to hand out.
	_, raw := apiCall(t, rt, "GET", "/v1/settings", "")
	if secret, _ := st.GetSettings(); secret != nil && secret.PanelSecretPath != "" &&
		strings.Contains(string(raw), secret.PanelSecretPath) {
		t.Error("GET /v1/settings leaked the panel secret path")
	}
}

// The master's routing has to round-trip through /v1: an integration that reads it,
// changes a rule and writes it back must get exactly what it sent, or it cannot be used
// to manage a server at all.
func TestAPIServerRoutingRoundTrip(t *testing.T) {
	rt, _ := apiTestRouter(t)

	const body = `{"routing":{"block_ads":true,"block_bittorrent":true,"block_domains":["ads.example.com"]},` +
		`"xray_dns":"1.1.1.1","warp_enabled":false,"opera_enabled":false,"opera_country":"EU"}`
	code, data := apiCall(t, rt, "POST", "/v1/servers/0/routing", body)
	if code != http.StatusOK {
		t.Fatalf("POST routing = %d: %s", code, data)
	}

	code, data = apiCall(t, rt, "GET", "/v1/servers/0/routing", "")
	if code != http.StatusOK {
		t.Fatalf("GET routing = %d: %s", code, data)
	}
	var got apiServerRouting
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, data)
	}
	if !got.Routing.BlockAds || !got.Routing.BlockBittorrent {
		t.Errorf("block flags did not survive: %+v", got.Routing)
	}
	if len(got.Routing.BlockDomains) != 1 || got.Routing.BlockDomains[0] != "ads.example.com" {
		t.Errorf("block_domains did not survive: %v", got.Routing.BlockDomains)
	}
	if got.XrayDNS != "1.1.1.1" {
		t.Errorf("xray_dns = %q, want 1.1.1.1", got.XrayDNS)
	}

	// An unknown server is a 404, not a silent success against the master.
	if code, _ := apiCall(t, rt, "GET", "/v1/servers/9999/routing", ""); code != http.StatusNotFound {
		t.Errorf("GET routing for a missing server = %d, want 404", code)
	}
}
