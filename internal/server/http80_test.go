package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AppsGanin/rospanel/internal/decoy"
	"github.com/AppsGanin/rospanel/internal/tlsmgr"
)

// Port 80 exists so the host does not look like one that serves TLS and nothing
// else. That only holds if it answers the way the thing on 443 claims to be.
func TestRedirectImitatesTheDecoysServer(t *testing.T) {
	h := RedirectHandler("panel.example")
	for _, tc := range []struct {
		name, host, target, want string
	}{
		{"plain path", "vpn.example", "/", "https://vpn.example/"},
		{"query is carried", "vpn.example", "/a/b?c=1&d=2", "https://vpn.example/a/b?c=1&d=2"},
		{"port is stripped", "vpn.example:80", "/x", "https://vpn.example/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.target, nil)
			r.Host = tc.host
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			// Caddy answers its automatic HTTP→HTTPS redirect with 308 and no body;
			// nginx would say 301. The decoy on 443 says it is Caddy.
			if w.Code != http.StatusPermanentRedirect {
				t.Errorf("status %d, want %d — the server this imitates uses 308",
					w.Code, http.StatusPermanentRedirect)
			}
			if got := w.Header().Get("Location"); got != tc.want {
				t.Errorf("Location %q, want %q", got, tc.want)
			}
			if got := w.Header().Get("Server"); got != decoy.ServerName {
				t.Errorf("Server %q, want %q — 80 and 443 must not name different servers",
					got, decoy.ServerName)
			}
			if w.Body.Len() != 0 {
				t.Errorf("body is %d bytes, want none", w.Body.Len())
			}
		})
	}
}

// The failure this guards is silent and delayed: a CA does not follow a redirect to
// 443, so a challenge answered with "308, go to HTTPS" fails validation, renewal
// stops working, and nothing shows it until the certificate expires weeks later.
func TestChallengeIsServedNotRedirected(t *testing.T) {
	const token, keyAuth = "tok-123", "tok-123.thumbprint"
	tlsmgr.PresentForTest(token, keyAuth)
	h := RedirectHandler("panel.example")

	r := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/"+token, nil)
	r.Host = "vpn.example"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — the CA gets a redirect it will not follow", w.Code)
	}
	if w.Body.String() != keyAuth {
		t.Errorf("body %q, want the key authorization %q", w.Body.String(), keyAuth)
	}

	// An unknown token under the same path must not be redirected either: sending a
	// CA to HTTPS reads as a broken challenge rather than a missing one.
	r = httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/nope", nil)
	r.Host = "vpn.example"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown token: status %d, want 404", w.Code)
	}
}

// Taking port 80 must switch ACME over to answering through this listener. If it
// does not, lego tries to bind a port we are already holding and renewal fails.
func TestHoldingPortEightyRedirectsACMEThroughIt(t *testing.T) {
	if tlsmgr.SharedHTTP01() {
		t.Fatal("shared HTTP-01 was already on before anything took port 80")
	}
	srv := StartRedirector("127.0.0.1:0", "panel.example")
	if srv == nil {
		t.Skip("could not bind a port in this environment")
	}
	if !tlsmgr.SharedHTTP01() {
		t.Error("port 80 is held but ACME would still try to bind it itself")
	}
	_ = srv.Close()
	// Serve returns once closed; give the goroutine its turn to clear the flag.
	for i := 0; i < 100 && tlsmgr.SharedHTTP01(); i++ {
		//nolint:staticcheck // a short spin is cheaper here than plumbing a channel out
	}
}
