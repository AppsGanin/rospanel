package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The public Swagger UI lives on another origin, so a "Try it out" call is a
// cross-origin request with an Authorization header — the browser sends a
// preflight first, and without an answer every call fails before it reaches us.
func TestAPICORSPreflight(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	base, _ := apiFixture(t, h, st)

	req := httptest.NewRequest(http.MethodOptions, base+"/v1/users", nil)
	req.Header.Set("Origin", "https://appsganin.github.io")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("allow-origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "authorization") {
		t.Errorf("allow-headers = %q, no authorization", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPatch) {
		t.Errorf("allow-methods = %q, no PATCH", got)
	}
}
