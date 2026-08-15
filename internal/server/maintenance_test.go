package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Maintenance mode takes the PUBLIC surfaces down (subscription, decoy) with a 503
// while the PANEL stays reachable — otherwise the operator who turned it on could
// never turn it off.
func TestMaintenanceModeGatesPublicButNotPanel(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "maint", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_ = st
	rt := h.(*Router)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", "curl/8")
		req.RemoteAddr = testClientIP + ":40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Off: the subscription answers normally.
	if rec := get("/sub/" + u.SubToken); rec.Code != http.StatusOK {
		t.Fatalf("sub before maintenance: status %d", rec.Code)
	}

	rt.setMaintenance(true)

	// On: the subscription now serves the maintenance page (503), and does NOT leak
	// the user's real config.
	rec := get("/sub/" + u.SubToken)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("sub during maintenance: status %d, want 503", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, u.UUID) {
		t.Error("the maintenance page leaked the real subscription")
	}

	// The panel secret path must still route to the panel (not the 503 page) — the
	// operator has to be able to switch it back off.
	if rec := get("/secretpath/"); rec.Code == http.StatusServiceUnavailable {
		t.Error("the panel is behind the maintenance page — the operator is locked out")
	}

	rt.setMaintenance(false)
	if rec := get("/sub/" + u.SubToken); rec.Code != http.StatusOK {
		t.Errorf("sub after maintenance off: status %d, want 200", rec.Code)
	}
}
