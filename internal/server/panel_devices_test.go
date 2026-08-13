package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// panelPost issues an authenticated panel request. The panel mux is mounted under
// the secret path and gated by a session, so these go through the manager directly
// where a session would be the only thing under test.
func TestUnbindKeepsUsersApart(t *testing.T) {
	_, mgr, st := nodeAPITestServer(t)

	a, err := mgr.CreateUser(t.Context(), "alice", 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b, err := mgr.CreateUser(t.Context(), "bob", 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The same install id on two accounts is not a collision — it is one device that
	// holds two subscriptions, which is ordinary. Releasing one must not touch the
	// other.
	for _, id := range []int64{a.ID, b.ID} {
		if _, err := st.RegisterDevice(id, model.Device{HWID: "shared", LastSeen: 1}, 0); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	ok, err := mgr.UnbindDevice(t.Context(), a.ID, "shared")
	if err != nil || !ok {
		t.Fatalf("unbind: ok=%v err=%v", ok, err)
	}
	if n, _ := st.CountDevices(b.ID); n != 1 {
		t.Errorf("bob has %d devices after alice unbound hers, want 1", n)
	}
	// Unbinding something that was never bound is a 'no', not a silent success —
	// the handler answers 404 off this.
	if ok, _ := mgr.UnbindDevice(t.Context(), a.ID, "shared"); ok {
		t.Error("unbinding an already-released device reported a removal")
	}
}

// The limits endpoint is reached by the bots and by older integrations that know
// nothing about speed caps. A body without the field must leave the cap alone —
// reading "absent" as "zero" would quietly lift every cap the first time somebody
// edited a quota.
func TestSetUserLimitsLeavesSpeedAloneWhenAbsent(t *testing.T) {
	_, mgr, st := nodeAPITestServer(t)

	u, err := mgr.CreateUser(t.Context(), "capped", 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := mgr.SetUserSpeedLimit(t.Context(), u.ID, 5000); err != nil {
		t.Fatalf("set speed: %v", err)
	}

	// What the panel handler decodes: a body with no speed_limit key at all.
	var req struct {
		DataLimit   int64 `json:"data_limit"`
		ExpireAt    int64 `json:"expire_at"`
		DeviceLimit int   `json:"device_limit"`
		SpeedLimit  *int  `json:"speed_limit"`
	}
	if err := json.Unmarshal([]byte(`{"data_limit":1073741824,"expire_at":0,"device_limit":2}`), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.SpeedLimit != nil {
		t.Fatalf("an absent speed_limit decoded to %v, want nil", *req.SpeedLimit)
	}
	if err := mgr.SetUserLimits(t.Context(), u.ID, req.DataLimit, req.ExpireAt, req.DeviceLimit); err != nil {
		t.Fatalf("set limits: %v", err)
	}
	fresh, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fresh.SpeedLimit != 5000 {
		t.Errorf("speed cap = %d after a limits write that never mentioned it, want 5000", fresh.SpeedLimit)
	}
	if fresh.DeviceLimit != 2 || fresh.DataLimit != 1<<30 {
		t.Errorf("the limits that WERE sent didn't land: %+v", fresh)
	}
}

// A cap can be lifted, and lifting it must reach the store — "0 means unlimited"
// only works if zero is actually written.
func TestSetUserSpeedLimitRejectsNegativeAndClearsAtZero(t *testing.T) {
	_, mgr, st := nodeAPITestServer(t)

	u, err := mgr.CreateUser(t.Context(), "u", 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := mgr.SetUserSpeedLimit(t.Context(), u.ID, -1); err == nil {
		t.Error("a negative cap was accepted")
	}
	if err := mgr.SetUserSpeedLimit(t.Context(), u.ID, 2000); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := mgr.SetUserSpeedLimit(t.Context(), u.ID, 0); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if fresh, _ := st.GetUser(u.ID); fresh.SpeedLimit != 0 {
		t.Errorf("cap = %d after being lifted, want 0", fresh.SpeedLimit)
	}
}

// Whatever a client puts in the headers ends up in the panel UI, the subscription
// page and a Telegram message. It must arrive as one line of bounded text.
func TestDeviceHeadersAreSanitised(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u := hwidUser(t, mgr, st, 5, false)

	req := httptest.NewRequest(http.MethodGet, "/sub/"+u.SubToken, nil)
	req.Header.Set("User-Agent", "Happ/1.0")
	req.Header.Set(model.HeaderHWID, "  id-with-space  ")
	req.Header.Set(model.HeaderDeviceModel, strings.Repeat("M", 500))
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	devices, err := st.ListDevices(u.ID)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices = %+v, err %v", devices, err)
	}
	d := devices[0]
	if d.HWID != "id-with-space" {
		t.Errorf("hwid = %q, want it trimmed", d.HWID)
	}
	if len(d.Model) > 64 {
		t.Errorf("model kept %d characters, want it capped", len(d.Model))
	}
}
