package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The docs page and its shell must load entirely from our own origin — a CDN link
// renders a half-drawn page where that CDN is blocked and leaks who opened it, the
// same rule the decoys and the subscription page already follow.
func TestSwaggerUISelfHosted(t *testing.T) {
	h, _, st := nodeAPITestServer(t)
	base, _ := apiFixture(t, h, st)

	// The docs page is key-free and references only relative assets.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, base+"/v1/docs", nil)
	req.RemoteAddr = testClientIP + ":40000"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("docs page status %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "jsdelivr") || strings.Contains(body, "http://") ||
		strings.Contains(body, "https://") {
		t.Errorf("docs page still references an external origin:\n%s", body)
	}

	// Both assets are served locally with a sane content type.
	for _, a := range []struct{ path, ct string }{
		{"/v1/swagger-ui.css", "text/css"},
		{"/v1/swagger-ui-bundle.js", "text/javascript"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, base+a.path, nil)
		req.RemoteAddr = testClientIP + ":40000"
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status %d", a.path, rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), a.ct) {
			t.Errorf("%s content-type %q, want %s", a.path, rec.Header().Get("Content-Type"), a.ct)
		}
		if rec.Body.Len() < 1000 {
			t.Errorf("%s suspiciously small (%d bytes) — asset missing?", a.path, rec.Body.Len())
		}
	}
}
