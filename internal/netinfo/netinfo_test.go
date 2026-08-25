package netinfo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveFromURLs(t *testing.T) {
	// 1. Success on first server
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "203.0.113.50\n")
	}))
	defer srv1.Close()

	client := srv1.Client()
	client.Timeout = 2 * time.Second

	ip := resolveFromURLs(client, []string{srv1.URL})
	if ip != "203.0.113.50" {
		t.Errorf("resolveFromURLs = %q; want 203.0.113.50", ip)
	}

	// 2. First server returns 500 / 429, second server returns valid IP
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
	}))
	defer srv500.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "198.51.100.25")
	}))
	defer srv2.Close()

	ip2 := resolveFromURLs(client, []string{srv500.URL, srv2.URL})
	if ip2 != "198.51.100.25" {
		t.Errorf("resolveFromURLs with fallback = %q; want 198.51.100.25", ip2)
	}

	// 3. All servers return invalid text or error codes
	srvGarbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>Error</html>")
	}))
	defer srvGarbage.Close()

	ip3 := resolveFromURLs(client, []string{srv500.URL, srvGarbage.URL})
	if ip3 != "" {
		t.Errorf("resolveFromURLs on all invalid = %q; want empty string", ip3)
	}
}

func TestPublicIPFallback(t *testing.T) {
	// Should return either public IP or empty/local without crashing
	_ = PublicIP()
}
