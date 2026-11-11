package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The reported case: "no limit" by address, the panel default at 1, a second app
// refused. The HWID limit is its own number now, and -1 lifts it.
func TestHWIDLimitIsSeparateFromTheAddressLimit(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u := hwidUser(t, mgr, st, 0, false)
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	set.HWIDFallbackLimit = 1
	if err := st.SetHWIDSettings(set); err != nil {
		t.Fatal(err)
	}
	if rec := fetchSub(h, u.SubToken, "phone"); rec.Code != http.StatusOK {
		t.Fatalf("first device: status %d", rec.Code)
	}
	if rec := fetchSub(h, u.SubToken, "mac"); rec.Code != http.StatusForbidden {
		t.Fatalf("second device on the default of 1: status %d, want 403", rec.Code)
	}
	if err := mgr.SetUserHWIDLimit(t.Context(), u.ID, model.HWIDLimitUnlimited); err != nil {
		t.Fatal(err)
	}
	if rec := fetchSub(h, u.SubToken, "mac"); rec.Code != http.StatusOK {
		t.Fatalf("second device with no HWID limit: status %d, want 200", rec.Code)
	}
	if err := mgr.SetUserHWIDLimit(t.Context(), u.ID, -2); err == nil {
		t.Error("hwid_limit -2 accepted")
	}
}

// An integration that only knows device_limit keeps moving both while they agree,
// and leaves a limit the operator split alone.
func TestAPIPatchDeviceLimitCarriesTheHWIDLimitWhileInStep(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	u, err := mgr.CreateUser(t.Context(), "both", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	patch := func(body string) {
		t.Helper()
		if rec := apiDo(t, h, http.MethodPatch, base+"/v1/users/"+uid(u.ID), key, body); rec.Code != http.StatusOK {
			t.Fatalf("patch %s: status %d, body %s", body, rec.Code, rec.Body.String())
		}
	}
	get := func() model.User {
		t.Helper()
		got, err := st.GetUser(u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return *got
	}
	patch(`{"device_limit":3}`)
	if g := get(); g.DeviceLimit != 3 || g.HWIDLimit != 3 {
		t.Fatalf("in step: device %d hwid %d, want 3 and 3", g.DeviceLimit, g.HWIDLimit)
	}
	patch(`{"hwid_limit":-1}`)
	patch(`{"device_limit":5}`)
	if g := get(); g.DeviceLimit != 5 || g.HWIDLimit != -1 {
		t.Fatalf("split: device %d hwid %d, want 5 and -1", g.DeviceLimit, g.HWIDLimit)
	}
	if rec := apiDo(t, h, http.MethodPatch, base+"/v1/users/"+uid(u.ID), key, `{"hwid_limit":101}`); rec.Code != http.StatusBadRequest {
		t.Errorf("hwid_limit 101: status %d, want 400", rec.Code)
	}
}

// A plan saved without hwid_limit gives the device limit as its HWID limit, as the
// one number did; a plan that names it keeps its own.
func TestAPIPlanWithoutHWIDLimitFollowsDevices(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	save := func(body string) model.TariffPlan {
		t.Helper()
		rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/plans", key, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("save %s: status %d, body %s", body, rec.Code, rec.Body.String())
		}
		var out struct {
			Data model.TariffPlan `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data
	}
	if p := save(`{"slug":"old","name":"Old","price_rub":100,"period_days":30,"device_limit":3,"enabled":true}`); p.HWIDLimit != 3 {
		t.Errorf("no hwid_limit: got %d, want 3", p.HWIDLimit)
	}
	if p := save(`{"slug":"new","name":"New","price_rub":100,"period_days":30,"device_limit":3,"hwid_limit":-1,"enabled":true}`); p.HWIDLimit != -1 {
		t.Errorf("hwid_limit -1: got %d", p.HWIDLimit)
	}
}

// The address default is a setting of its own, read back as written.
func TestAPISettingsIPFallbackLimit(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	if rec := apiDo(t, h, http.MethodPatch, base+"/v1/settings", key, `{"ip_fallback_limit":2}`); rec.Code != http.StatusOK {
		t.Fatalf("patch: status %d, body %s", rec.Code, rec.Body.String())
	}
	set, err := st.GetSettings()
	if err != nil || set.IPFallbackLimit != 2 {
		t.Fatalf("stored %d (%v), want 2", set.IPFallbackLimit, err)
	}
	if rec := apiDo(t, h, http.MethodPatch, base+"/v1/settings", key, `{"ip_fallback_limit":-1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("-1: status %d, want 400", rec.Code)
	}
}

// The panel's limits save refuses an HWID limit out of range before writing, and the
// refusal carries the number its message names.
func TestPanelLimitsRefuseABadHWIDLimitWithItsMax(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	c := signIn(t, st, "op", model.RoleOperator, false)
	if code, errCode := send(t, rt, http.MethodPost, "/api/users", `{"name":"lim"}`, c, nil); code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, errCode)
	}
	users, _ := st.ListUsers()
	path := "/api/users/" + uid(users[0].ID) + "/limits"
	req := httptest.NewRequest(http.MethodPost, path,
		strings.NewReader(`{"data_limit":1024,"device_limit":0,"hwid_limit":101}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	w := httptest.NewRecorder()
	rt.panelMux().ServeHTTP(w, req)
	var env struct {
		Code string         `json:"code"`
		Args map[string]any `json:"args"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if w.Code != http.StatusBadRequest || env.Code != "err.badHWIDLimit" || env.Args["max"] != float64(model.MaxDeviceLimit) {
		t.Fatalf("got %d %s args %v, want 400 err.badHWIDLimit with max %d", w.Code, env.Code, env.Args, model.MaxDeviceLimit)
	}
	if u, _ := st.GetUser(users[0].ID); u.DataLimit != 0 {
		t.Errorf("the refused save still wrote the quota: %d", u.DataLimit)
	}
}

// /v1 takes -1 as "no limit" for both and refuses anything below it, on create and
// on patch, before anything is written.
func TestAPIDeviceLimitRange(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/users", key, `{"name":"neg","device_limit":-2}`); rec.Code != http.StatusBadRequest {
		t.Errorf("create device_limit -2: status %d, want 400", rec.Code)
	}
	if users, _ := st.ListUsers(); len(users) != 0 {
		t.Fatalf("a refused create still made %d users", len(users))
	}
	u, err := mgr.CreateUser(t.Context(), "r", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rec := apiDo(t, h, http.MethodPatch, base+"/v1/users/"+uid(u.ID), key, `{"device_limit":-2}`); rec.Code != http.StatusBadRequest {
		t.Errorf("patch device_limit -2: status %d, want 400", rec.Code)
	}
	if rec := apiDo(t, h, http.MethodPatch, base+"/v1/users/"+uid(u.ID), key, `{"device_limit":-1}`); rec.Code != http.StatusOK {
		t.Fatalf("patch device_limit -1: status %d, body %s", rec.Code, rec.Body.String())
	}
	if g, _ := st.GetUser(u.ID); g.DeviceLimit != -1 || g.IPLimit != 0 {
		t.Errorf("device %d ip %d, want -1 and no limit in force", g.DeviceLimit, g.IPLimit)
	}
}
