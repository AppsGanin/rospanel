package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/AppsGanin/rospanel/internal/store"
)

// uid renders a user id for a URL.
func uid(id int64) string { return strconv.FormatInt(id, 10) }

// apiFixture stands the external surface up on a known path and returns a working
// key. Every device test needs the same three lines otherwise.
func apiFixture(t *testing.T, h http.Handler, st *store.Store) (base, key string) {
	t.Helper()
	if err := st.SetAPIPath("dev-api"); err != nil {
		t.Fatalf("api path: %v", err)
	}
	h.(*Router).setAPIPath("dev-api")
	k, err := st.CreateAPIKey("devices-test")
	if err != nil {
		t.Fatalf("api key: %v", err)
	}
	return "/dev-api", k.RawKey
}

func apiGet(t *testing.T, h http.Handler, target, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPIListsAndUnbindsDevices(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u := hwidUser(t, mgr, st, 2, false)
	base, key := apiFixture(t, h, st)

	for _, hwid := range []string{"dev-a", "dev-b"} {
		if rec := fetchSub(h, u.SubToken, hwid); rec.Code != http.StatusOK {
			t.Fatalf("bind %s: status %d", hwid, rec.Code)
		}
	}

	rec := apiGet(t, h, base+"/v1/users/"+uid(u.ID)+"/devices", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d, body %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Data apiDeviceList `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(listed.Data.Devices) != 2 {
		t.Errorf("%d devices listed, want 2", len(listed.Data.Devices))
	}
	// The cap travels with the list so a caller knows whether the roster is full
	// without fetching the user and the settings separately.
	if listed.Data.Limit != 2 || !listed.Data.Enabled {
		t.Errorf("limit=%d enabled=%v, want 2 and true", listed.Data.Limit, listed.Data.Enabled)
	}

	unbind := postJSON(t, h, base+"/v1/users/"+uid(u.ID)+"/devices/unbind", key,
		map[string]any{"hwid": "dev-a"})
	if unbind.Code != http.StatusOK {
		t.Fatalf("unbind: status %d, body %s", unbind.Code, unbind.Body.String())
	}
	if n, _ := st.CountDevices(u.ID); n != 1 {
		t.Errorf("%d devices left, want 1", n)
	}
	// The freed slot is real: a third device now fits where it was refused before.
	if rec := fetchSub(h, u.SubToken, "dev-c"); rec.Code != http.StatusOK {
		t.Errorf("new device after unbind: status %d, want 200", rec.Code)
	}

	all := postJSON(t, h, base+"/v1/users/"+uid(u.ID)+"/devices/unbind", key,
		map[string]any{"all": true})
	if all.Code != http.StatusOK {
		t.Fatalf("unbind all: status %d, body %s", all.Code, all.Body.String())
	}
	if n, _ := st.CountDevices(u.ID); n != 0 {
		t.Errorf("%d devices left after releasing all", n)
	}
}

func TestAPIDeviceErrors(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u := hwidUser(t, mgr, st, 1, false)
	base, key := apiFixture(t, h, st)

	// A body with neither hwid nor all is a caller mistake, not an empty success.
	if rec := postJSON(t, h, base+"/v1/users/"+uid(u.ID)+"/devices/unbind", key,
		map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: status %d, want 400", rec.Code)
	}
	// An id that was never bound is a 404 rather than a silent 200 — an integration
	// retrying blindly should be able to tell the difference.
	if rec := postJSON(t, h, base+"/v1/users/"+uid(u.ID)+"/devices/unbind", key,
		map[string]any{"hwid": "never-seen"}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown device: status %d, want 404", rec.Code)
	}
	// The surface is key-gated like the rest of /v1: the roster names a customer's
	// hardware.
	if rec := apiGet(t, h, base+"/v1/users/"+uid(u.ID)+"/devices", ""); rec.Code == http.StatusOK {
		t.Errorf("unauthenticated list answered 200: %s", rec.Body.String())
	}
}
