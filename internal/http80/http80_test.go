package http80

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/decoy"
	"github.com/Shu1t3/rospanel-shu1t3/internal/tlsmgr"
)

// Port 80 exists so the host does not look like one that serves TLS and nothing
// else. That only holds if it answers the way the thing on 443 claims to be.
func TestRedirectImitatesTheDecoysServer(t *testing.T) {
	h := Handler(func() string { return "vpn.example" })
	for _, tc := range []struct {
		name, host, target, want string
	}{
		{"plain path", "vpn.example", "/", "https://vpn.example/"},
		{"query is carried", "vpn.example", "/a/b?c=1&d=2", "https://vpn.example/a/b?c=1&d=2"},
		{"port is stripped", "vpn.example:80", "/x", "https://vpn.example/x"},
		// Whatever Host is asked for, the answer names this machine. 443 refuses an SNI
		// it cannot serve; a port 80 that echoed the asked-for name back would both
		// contradict that and let anyone choose where this panel points people.
		{"a foreign Host is not echoed", "evil.example", "/x", "https://vpn.example/x"},
		{"no scheme smuggling", "vpn.example", "//evil.example/", "https://vpn.example//evil.example/"},
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
	h := Handler(func() string { return "panel.example" })

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
	srv := Start("127.0.0.1:0", func() string { return "panel.example" })
	if srv == nil {
		t.Skip("could not bind a port in this environment")
	}
	if !tlsmgr.SharedHTTP01() {
		t.Error("port 80 is held but ACME would still try to bind it itself")
	}
	_ = srv.Close()
	// Serve returns once closed, and the goroutine clears the flag after it does — so
	// ACME goes back to binding the port itself rather than answering into a listener
	// that is gone.
	deadline := time.Now().Add(2 * time.Second)
	for tlsmgr.SharedHTTP01() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if tlsmgr.SharedHTTP01() {
		t.Error("the listener is gone but ACME still thinks challenges are served through it")
	}
}

// The operator can point a new domain at the box without restarting anything, and the
// redirect has to follow. Captured once, port 80 would keep naming the old host — the
// same contradiction between ports this whole listener exists to remove.
func TestRedirectFollowsAHostChange(t *testing.T) {
	current := "old.example"
	h := Handler(func() string { return current })

	ask := func() string {
		r := httptest.NewRequest(http.MethodGet, "/p", nil)
		r.Host = "old.example"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Header().Get("Location")
	}
	if got, want := ask(), "https://old.example/p"; got != want {
		t.Fatalf("Location %q, want %q", got, want)
	}
	current = "new.example"
	if got, want := ask(), "https://new.example/p"; got != want {
		t.Errorf("Location %q after the host changed, want %q", got, want)
	}
}
