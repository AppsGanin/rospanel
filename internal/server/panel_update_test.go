package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestCheckUpdateAuthAndResponseShape(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)

	// Anonymous request must be rejected.
	reqAnon := httptest.NewRequest("GET", "/api/update", nil)
	recAnon := httptest.NewRecorder()
	h.ServeHTTP(recAnon, reqAnon)
	if recAnon.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/update anonymously = %d, want 401", recAnon.Code)
	}

	// Authenticated request should return current version and structure.
	req := httptest.NewRequest("GET", "/api/update", nil)
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/update = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Current   string  `json:"current"`
		Latest    *string `json:"latest"`
		Available bool    `json:"available"`
		Notes     *string `json:"notes"`
		Error     *string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal /api/update response: %v\nbody: %s", err, rec.Body.String())
	}

	if resp.Current == "" {
		t.Errorf("expected current version in /api/update response, got empty")
	}
}
