package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/core"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

// hwidUser creates a user with the given device cap and turns device binding on.
func hwidUser(t *testing.T, mgr *core.Manager, st *store.Store, capacity int, require bool) model.User {
	t.Helper()
	u, err := mgr.CreateUser(t.Context(), "hwid-user", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := mgr.SetUserLimits(t.Context(), u.ID, 0, 0, capacity); err != nil {
		t.Fatalf("set limits: %v", err)
	}
	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	set.HWIDEnabled, set.HWIDRequire = true, require
	if err := st.SetHWIDSettings(set); err != nil {
		t.Fatalf("save hwid settings: %v", err)
	}
	fresh, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	return *fresh
}

// fetchSub asks for the machine payload the way a client does, with the device
// headers Happ and v2RayTun send.
func fetchSub(h http.Handler, token, hwid string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/sub/"+token, nil)
	req.Header.Set("User-Agent", "Happ/1.0")
	if hwid != "" {
		req.Header.Set(model.HeaderHWID, hwid)
		req.Header.Set(model.HeaderDeviceOS, "android")
		req.Header.Set(model.HeaderDeviceModel, "Pixel 9")
	}
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSubscriptionBindsDevicesUpToTheCap(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u := hwidUser(t, mgr, st, 2, false)

	for _, hwid := range []string{"dev-a", "dev-b"} {
		if rec := fetchSub(h, u.SubToken, hwid); rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", hwid, rec.Code)
		}
	}
	// A repeat fetch from a bound device must keep working with the roster full.
	if rec := fetchSub(h, u.SubToken, "dev-a"); rec.Code != http.StatusOK {
		t.Errorf("bound device refused on refetch: status %d", rec.Code)
	}

	rec := fetchSub(h, u.SubToken, "dev-c")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("third device: status %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "2") {
		t.Errorf("refusal body doesn't say how many devices are bound: %q", rec.Body.String())
	}
	devices, err := st.ListDevices(u.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 2 {
		t.Errorf("%d devices bound, want 2", len(devices))
	}
	if devices[0].Model == "" || devices[0].OS == "" {
		t.Errorf("device headers not recorded: %+v", devices[0])
	}
}

// A client that sends no id gets no subscription: that is the default, because a cap
// a user can dodge by switching to a quieter client is not a cap. Turning the switch
// off serves them again, counted by address as before.
func TestSubscriptionRefusesClientsWithoutHWID(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u := hwidUser(t, mgr, st, 1, true)

	if rec := fetchSub(h, u.SubToken, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("client with no id: status %d, want 403", rec.Code)
	}

	set, _ := st.GetSettings()
	set.HWIDRequire = false
	if err := st.SetHWIDSettings(set); err != nil {
		t.Fatalf("save: %v", err)
	}
	if rec := fetchSub(h, u.SubToken, ""); rec.Code != http.StatusOK {
		t.Errorf("with the requirement off: status %d, want 200", rec.Code)
	}
}

// The cap only applies once the operator turns the feature on; until then nothing
// is bound at all, so upgrading the panel changes nothing for anyone.
func TestSubscriptionIgnoresDevicesWhenDisabled(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u := hwidUser(t, mgr, st, 1, false)
	set, _ := st.GetSettings()
	set.HWIDEnabled = false
	if err := st.SetHWIDSettings(set); err != nil {
		t.Fatalf("save: %v", err)
	}

	for _, hwid := range []string{"dev-a", "dev-b", "dev-c"} {
		if rec := fetchSub(h, u.SubToken, hwid); rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", hwid, rec.Code)
		}
	}
	if n, _ := st.CountDevices(u.ID); n != 0 {
		t.Errorf("%d devices bound while the feature is off", n)
	}
}

// The page lists the devices and the button releases one — the self-service that
// keeps a full roster from becoming a support ticket.
func TestSubscriptionPageListsAndUnbindsDevices(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u := hwidUser(t, mgr, st, 2, false)
	if rec := fetchSub(h, u.SubToken, "dev-a"); rec.Code != http.StatusOK {
		t.Fatalf("bind: status %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/sub/"+u.SubToken, nil)
	req.Header.Set("Accept", "text/html")
	req.RemoteAddr = testClientIP + ":40000"
	page := httptest.NewRecorder()
	h.ServeHTTP(page, req)
	if page.Code != http.StatusOK {
		t.Fatalf("page: status %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "Pixel 9") {
		t.Error("the page doesn't list the bound device")
	}
	// The unbind target lives in a <script>, where html/template escapes the slashes
	// — match on the row and the handler instead of the raw path.
	if !strings.Contains(page.Body.String(), `data-hwid="dev-a"`) {
		t.Error("the device row carries no id to unbind")
	}
	if !strings.Contains(page.Body.String(), "unbindDevice(") {
		t.Error("the page has no unbind button")
	}

	body, _ := json.Marshal(map[string]string{"hwid": "dev-a"})
	unbind := httptest.NewRequest(http.MethodPost, "/sub/"+u.SubToken+"/devices/unbind", strings.NewReader(string(body)))
	unbind.Header.Set("Content-Type", "application/json")
	unbind.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, unbind)
	if rec.Code != http.StatusOK {
		t.Fatalf("unbind: status %d, body %s", rec.Code, rec.Body.String())
	}
	if n, _ := st.CountDevices(u.ID); n != 0 {
		t.Errorf("%d devices left after unbinding the only one", n)
	}
}
