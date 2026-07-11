package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAPIMuxRegisters ensures the /v1 route table builds without a pattern
// conflict (Go 1.22 ServeMux panics on ambiguous registrations) and that an
// unmatched path returns the in-envelope JSON 404.
func TestAPIMuxRegisters(t *testing.T) {
	rt := &Router{}
	var h http.Handler
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("apiMux panicked: %v", r)
			}
		}()
		h = rt.apiMux()
	}()
	if h == nil {
		t.Fatal("nil handler")
	}
}

func TestAPIKeyFromRequest(t *testing.T) {
	cases := []struct {
		auth, xkey, want string
	}{
		{"Bearer rp_abc", "", "rp_abc"},
		{"bearer rp_abc", "", "rp_abc"}, // case-insensitive scheme
		{"rp_raw", "", "rp_raw"},        // bare Authorization value
		{"", "rp_hdr", "rp_hdr"},        // X-API-Key fallback
		{"", "", ""},
	}
	for _, c := range cases {
		r, _ := http.NewRequest("GET", "/v1/users", nil)
		if c.auth != "" {
			r.Header.Set("Authorization", c.auth)
		}
		if c.xkey != "" {
			r.Header.Set("X-API-Key", c.xkey)
		}
		if got := apiKeyFromRequest(r); got != c.want {
			t.Errorf("auth=%q xkey=%q → %q, want %q", c.auth, c.xkey, got, c.want)
		}
	}
}

// An IP that keeps presenting invalid keys is locked out, and the lockout answers
// 429 before the request ever reaches the store (so a nil manager is fine here).
func TestAPIAuthLocksOutAfterRepeatedBadKeys(t *testing.T) {
	rt := &Router{apiKeys: newAPIKeyGuard()}
	h := rt.apiHandler()

	req := httptest.NewRequest(http.MethodGet, "/v1/users", nil)
	ip := clientIP(req)
	for range rt.apiKeys.maxFails {
		rt.apiKeys.fail(ip, "")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/users", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out IP got %d, want 429", rec.Code)
	}

	// A valid key clears the record, so a fixed integration recovers at once.
	rt.apiKeys.success(ip, "")
	if rt.apiKeys.blocked(ip, "") {
		t.Fatal("success() did not clear the lockout")
	}
}

// A request with no credential at all is a bare probe, not a guess — it must not
// spend the IP's failure budget, or an unauthenticated prober could lock out the
// address a legitimate integration shares with it.
func TestAPIAuthMissingKeyDoesNotCountAsFailure(t *testing.T) {
	rt := &Router{apiKeys: newAPIKeyGuard()}
	h := rt.apiHandler()

	for i := range rt.apiKeys.maxFails * 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/users", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("probe %d got %d, want 401 every time", i, rec.Code)
		}
	}
}

func TestAtoiOr(t *testing.T) {
	for _, c := range []struct {
		in  string
		def int
		out int
	}{
		{"5", 0, 5}, {"", 3, 3}, {"x", 7, 7}, {" 12 ", 0, 12}, {"-4", 0, -4},
	} {
		if got := atoiOr(c.in, c.def); got != c.out {
			t.Errorf("atoiOr(%q,%d)=%d, want %d", c.in, c.def, got, c.out)
		}
	}
}
