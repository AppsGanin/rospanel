package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The support relay's settings, driven through the real panel mux: handler →
// manager → store → the hand-written positional SELECT/Scan in store.GetSettings,
// where a column added in the wrong position is invisible to a unit test of either
// end alone.

// postPanelJSON runs a session-authenticated panel POST and returns the status and
// body (node_api_test.go's postJSON is the bearer-authenticated node counterpart).
func postPanelJSON(t *testing.T, h http.Handler, path, body string, c *http.Cookie) (int, string) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// saveBody builds a full /api/telegram payload. The endpoint configures all three
// bots in one request and validates them in order, so a partial body is rejected by
// whichever bot comes first — the SPA always sends every field, and so must a test
// that means to exercise the support half.
func saveBody(support string) string {
	return `{"enabled": false, "token": "", "backup_cron": "",
	         "user_enabled": false, "user_token": "", "user_reg_mode": "off",
	         "user_reg_code": "", ` + support + `}`
}

func getTelegramJSON(t *testing.T, h http.Handler, c *http.Cookie) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/telegram", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/telegram = %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestSupportSettingsRoundTripHTTP saves the support fields and reads them back out
// through the API, which is the path a misaligned settings column would break.
func TestSupportSettingsRoundTripHTTP(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)

	got := getTelegramJSON(t, h, admin)
	if got["support_enabled"] != false || got["support_group_id"] != float64(0) {
		t.Fatalf("unexpected support defaults: %+v", got)
	}

	// Saved disabled: no @username is resolved (getMe is never called with an empty
	// token), and that must be allowed — the operator is filling the form in stages.
	code, body := postPanelJSON(t, h, "/api/telegram", saveBody(
		`"support_enabled": false,
		 "support_token": "555:CCC",
		 "support_group_id": -1001234567890,
		 "support_greeting": "Опишите проблему"`), admin)
	if code != http.StatusOK {
		t.Fatalf("save = %d: %s", code, body)
	}

	got = getTelegramJSON(t, h, admin)
	if got["support_token"] != "555:CCC" {
		t.Errorf("token = %v, want 555:CCC", got["support_token"])
	}
	if got["support_group_id"] != float64(-1001234567890) {
		t.Errorf("group id = %v, want -1001234567890", got["support_group_id"])
	}
	if got["support_greeting"] != "Опишите проблему" {
		t.Errorf("greeting = %v", got["support_greeting"])
	}

	// The columns the support ones were appended after must still hold their
	// defaults: a positional Scan that drifted shows up as a neighbour picking up a
	// support value (or a zero) instead.
	set, err := rt.mgr.Settings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if set.GeoRefreshHours != 168 || set.IPListRefreshHours != 168 || set.MasterLabel != "" {
		t.Errorf("neighbouring settings disturbed: geo=%d iplist=%d master=%q",
			set.GeoRefreshHours, set.IPListRefreshHours, set.MasterLabel)
	}
}

// TestSupportEnableRequiresConfig covers the refusals that keep a half-configured
// relay from going live. Enabling with an unresolvable @username is the important
// one: the bots render the support entry point only for a non-empty username, so
// storing a blank would leave support switched on and no way in — with nothing on
// screen to explain why.
func TestSupportEnableRequiresConfig(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)

	cases := []struct {
		name, body, wantMsg string
	}{
		{
			name:    "no token",
			body:    saveBody(`"support_enabled": true, "support_group_id": -100123`),
			wantMsg: "укажите токен бота поддержки",
		},
		{
			name:    "no group",
			body:    saveBody(`"support_enabled": true, "support_token": "555:CCC"`),
			wantMsg: "укажите группу поддержки",
		},
		{
			name:    "malformed token",
			body:    saveBody(`"support_enabled": true, "support_token": "nope", "support_group_id": -100123`),
			wantMsg: "выглядит неверно",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := postPanelJSON(t, h, "/api/telegram", tc.body, admin)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", code, body)
			}
			if !strings.Contains(body, tc.wantMsg) {
				t.Errorf("message = %s, want it to mention %q", body, tc.wantMsg)
			}
		})
	}

	// Nothing was persisted by the refusals.
	set, err := rt.mgr.Settings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if set.TGSupportEnabled || set.TGSupportBotToken != "" {
		t.Fatalf("a refused save still persisted: %+v", set)
	}
}

// TestSupportCheckRefusesUnconfigured: the check button reaches out to Telegram, so
// it must fail fast and legibly before that when there's nothing to check yet.
func TestSupportCheckRefusesUnconfigured(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)

	code, body := postPanelJSON(t, h, "/api/telegram/support/check", `{}`, admin)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", code, body)
	}
	if !strings.Contains(body, "токен бота поддержки") {
		t.Errorf("message = %s", body)
	}
}

// A save that fails must change nothing. Persisting the three bots in sequence meant
// a failure on the last one left the first two written while the request reported an
// error — and the audit middleware skips failed requests, so nothing recorded it.
func TestFailedSaveLeavesEverythingUnchanged(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)

	code, body := postPanelJSON(t, h, "/api/telegram", saveBody(
		`"support_enabled": false, "support_token": "555:CCC", "support_group_id": -100123`), admin)
	if code != http.StatusOK {
		t.Fatalf("setup save = %d: %s", code, body)
	}

	// Now a request whose LAST section is invalid: the support token is malformed.
	// The first two sections carry values that must not survive the refusal.
	code, body = postPanelJSON(t, h, "/api/telegram", `{
		"enabled": false, "token": "999:ZZZ", "backup_cron": "",
		"user_enabled": false, "user_token": "888:YYY", "user_reg_mode": "off",
		"user_reg_code": "", "support_token": "сломанный-токен"
	}`, admin)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", code, body)
	}

	set, err := rt.mgr.Settings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if set.TGBotToken == "999:ZZZ" {
		t.Error("the admin bot token was written despite the request failing")
	}
	if set.TGUserBotToken == "888:YYY" {
		t.Error("the user bot token was written despite the request failing")
	}
	if set.TGSupportBotToken != "555:CCC" {
		t.Errorf("support token = %q, want the previously saved one", set.TGSupportBotToken)
	}
}

// Absent fields mean "unchanged", not "empty". A browser tab loaded before a field
// existed would otherwise wipe a bot token and get a 200 for it.
func TestPartialSaveKeepsUntouchedSections(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)

	code, body := postPanelJSON(t, h, "/api/telegram", saveBody(
		`"support_enabled": false, "support_token": "555:CCC",
		 "support_group_id": -100123, "support_greeting": "Опишите проблему"`), admin)
	if code != http.StatusOK {
		t.Fatalf("setup save = %d: %s", code, body)
	}

	// A body from an older client: it knows nothing about the support section.
	code, body = postPanelJSON(t, h, "/api/telegram", `{
		"enabled": false, "token": "", "backup_cron": "",
		"user_enabled": false, "user_token": "", "user_reg_mode": "off", "user_reg_code": ""
	}`, admin)
	if code != http.StatusOK {
		t.Fatalf("partial save = %d: %s", code, body)
	}

	set, err := rt.mgr.Settings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if set.TGSupportBotToken != "555:CCC" || set.TGSupportGroupID != -100123 ||
		set.TGSupportGreeting != "Опишите проблему" {
		t.Fatalf("an omitted section was wiped: %+v", set)
	}
}
