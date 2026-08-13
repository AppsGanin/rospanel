package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// metricsBody scrapes the endpoint with a valid key and returns the exposition text.
func metricsBody(t *testing.T, h http.Handler, base, key string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, base+"/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: status %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain exposition", ct)
	}
	return rec.Body.String()
}

func TestMetricsExposition(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	if _, err := mgr.CreateUser(t.Context(), "metrics-user", 0, 0); err != nil {
		t.Fatalf("create user: %v", err)
	}
	// The external surface is off until an operator gives it a path; a scrape target
	// only exists once they have.
	if err := st.SetAPIPath("metrics-api"); err != nil {
		t.Fatalf("api path: %v", err)
	}
	rt := h.(*Router)
	rt.setAPIPath("metrics-api")
	base := "/metrics-api"
	key, err := st.CreateAPIKey("scraper")
	if err != nil {
		t.Fatalf("api key: %v", err)
	}

	body := metricsBody(t, h, base, key.RawKey)
	for _, want := range []string{
		"# TYPE rospanel_users_total gauge",
		"rospanel_users_total 1",
		"# TYPE rospanel_traffic_bytes_total counter",
		`rospanel_traffic_bytes_total{direction="up"}`,
		"rospanel_xray_running",
		"rospanel_build_info{version=",
		`rospanel_node_online{node=`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition is missing %q\n%s", want, body)
		}
	}
	// A family header must appear exactly once however many samples it carries —
	// Prometheus rejects a duplicate TYPE line for the same name.
	if n := strings.Count(body, "# TYPE rospanel_traffic_bytes_total"); n != 1 {
		t.Errorf("TYPE line for rospanel_traffic_bytes_total appears %d times, want 1", n)
	}
}

// The scrape target must not be open: it reports user counts, host stats and the
// node roster, which is exactly the fingerprint the secret path exists to hide.
func TestMetricsRequiresKey(t *testing.T) {
	h, _, st := nodeAPITestServer(t)
	if err := st.SetAPIPath("metrics-api"); err != nil {
		t.Fatalf("api path: %v", err)
	}
	h.(*Router).setAPIPath("metrics-api")

	req := httptest.NewRequest(http.MethodGet, "/metrics-api/v1/metrics", nil)
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("unauthenticated scrape answered 200:\n%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "rospanel_users_total") {
		t.Error("metrics leaked to an unauthenticated caller")
	}
}

func TestEscapeLabel(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:      `plain`,
		`say "hi"`:   `say \"hi\"`,
		"two\nlines": `two\nlines`,
		`back\slash`: `back\\slash`,
	} {
		if got := escapeLabel(in); got != want {
			t.Errorf("escapeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
