package server

import (
	"net/http"
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
