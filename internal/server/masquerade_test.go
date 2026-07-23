package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// testClientIP is the fixed source these tests come from, so the per-IP limiters
// see one client.
const testClientIP = "203.0.113.7"

// getFrom issues a GET from testClientIP.
func getFrom(h http.Handler, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// requireExhausted fails the test unless the limiter is now refusing testClientIP.
// Without it these tests would pass on a limiter that never fired, proving nothing
// about what a throttled response looks like.
func requireExhausted(t *testing.T, l *ipRateLimiter) {
	t.Helper()
	if l.allow(testClientIP) {
		t.Fatal("limiter never refused the client — the throttled path was never exercised")
	}
}

// The subscription prefix defaults to the literal "sub", so unlike every other
// mounted segment it is known to an outsider in advance. When the per-IP throttle
// answered with net/http's bare 429 that was a free confirmation of the panel:
// fire enough requests at /sub/anything, and a reply no static site ever emits
// says a panel is here — no token, no domain, no guessing.
func TestSubscriptionThrottleStaysBehindTheDecoy(t *testing.T) {
	h, _, _ := nodeAPITestServer(t)
	rt := h.(*Router)

	baseline := getFrom(h, "/sub/no-such-token")

	var throttled *httptest.ResponseRecorder
	// The limiter allows 120/min; go well past it.
	for range 300 {
		rec := getFrom(h, "/sub/no-such-token")
		if rec.Code == http.StatusTooManyRequests {
			t.Fatal("throttled subscription request answered 429 — confirms the panel to anyone who floods a known path")
		}
		throttled = rec
	}
	requireExhausted(t, rt.subLimiter)

	// Whatever the limiter decided, the response must be the same decoy an
	// unknown path gets: same status, same body, same headers that matter.
	if throttled.Code != baseline.Code {
		t.Errorf("throttled status = %d, unthrottled = %d — the difference is the tell", throttled.Code, baseline.Code)
	}
	if throttled.Body.String() != baseline.Body.String() {
		t.Error("throttled body differs from the decoy's")
	}
	if got, want := throttled.Header().Get("Server"), baseline.Header().Get("Server"); got != want {
		t.Errorf("throttled Server = %q, want %q", got, want)
	}
}

// The node sync surface promises that a caller without a valid token cannot tell
// it from unknown hosting. A throttle reply would break that for anyone who
// floods it, so it too falls through to the decoy.
func TestNodeAPIThrottleStaysBehindTheDecoy(t *testing.T) {
	h, mgr, _ := nodeAPITestServer(t)
	rt := h.(*Router)
	if _, err := mgr.CreateNode("n1", "nl1.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	set, err := mgr.Store().GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	base := "/" + set.NodeAPIPath + "/v1/sync"

	baseline := getFrom(h, "/nothing-here")
	var throttled *httptest.ResponseRecorder
	// The node limiter allows 600/min.
	for range 900 {
		rec := getFrom(h, base)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatal("throttled node-API request answered 429")
		}
		throttled = rec
	}
	requireExhausted(t, rt.apiLimiter)
	if throttled.Code != baseline.Code || throttled.Body.String() != baseline.Body.String() {
		t.Error("throttled node-API response differs from what an unknown path returns")
	}
}
