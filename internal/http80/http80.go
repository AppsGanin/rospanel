// Package http80 answers plain HTTP on port 80 for the panel and for every node, so
// neither looks like a host that serves TLS and nothing else. Its own package because
// both need it and a node has no business importing the panel's HTTP layer to get it.
package http80

import (
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/decoy"
	"github.com/Shu1t3/rospanel-shu1t3/internal/tlsmgr"
)

// Handler answers plain HTTP on port 80: ACME challenges are served, and
// everything else is sent to HTTPS.
//
// The point is what port 80 looks like when nothing is there. A host that answers
// HTTPS on 443 with a plausible website and refuses 80 outright is a shape the real
// web does not have, and the refusal contradicts the decoy on 443 however good it
// is.
//
// The redirect imitates the server the decoy claims to be, down to the status code:
// Caddy answers its automatic HTTP→HTTPS redirect with 308 and an empty body
// where nginx would use 301. Claiming to be Caddy on 443 and redirecting like nginx
// on 80 is the same contradiction one layer down.
//
// host is the one name this machine answers to. The redirect goes there and not to
// whatever Host the request carried: 443 already refuses an SNI it cannot serve, so a
// port 80 that echoes any hostname back contradicts the port beside it and marks this
// as a catch-all redirector rather than a configured site. A single-site server
// sending everything to its own name is entirely ordinary. It also means no one can
// make this panel emit a Location pointing at a host of their choosing.
//
// Empty host (a first boot before the wizard has run) falls back to echoing, because
// there is nothing else to point at.
//
// Read per request rather than captured once: an operator who points a new domain at
// the box changes it without restarting anything, and a redirect still naming the old
// one is the same contradiction this exists to remove.
func Handler(host func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Before anything else: a CA validating a challenge will not follow a redirect
		// to 443, so this has to win over the redirect below.
		if tlsmgr.ServeHTTP01(w, r) {
			return
		}
		w.Header().Set("Server", decoy.ServerName)

		var target string
		if host != nil {
			target = host()
		}
		if target == "" {
			target = r.Host
		}
		if h, _, err := net.SplitHostPort(target); err == nil {
			target = h
		}
		if target == "" {
			// Nothing to redirect to. A server with no name for itself answers the
			// request rather than emitting a Location it cannot fill in.
			http.Error(w, "400 Bad Request", http.StatusBadRequest)
			return
		}
		// RequestURI, not Path: the query belongs to the redirect too.
		w.Header().Set("Location", "https://"+target+r.URL.RequestURI())
		// No body. http.Redirect would write a short HTML page for a GET, which the
		// server this imitates does not.
		w.WriteHeader(http.StatusPermanentRedirect)
	})
}

// Start serves Handler on addr (normally ":80") and tells tlsmgr
// that ACME challenges now go through it.
//
// Best-effort by design. Port 80 may be held by something the operator runs, and in
// that case we step aside: lego will go on binding the port itself for the few
// seconds a challenge takes, exactly as it did before this existed. Failing to bind
// must never be fatal — a cosmetic improvement to how the host looks is not worth a
// panel that will not start.
func Start(addr string, host func() string) *http.Server {
	if strings.TrimSpace(addr) == "" {
		addr = ":80"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("http80: not listening on %s (%v) — ACME keeps binding it per challenge", addr, err)
		return nil
	}
	srv := &http.Server{
		Handler:           Handler(host),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	tlsmgr.UseSharedHTTP01(true)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http80: %v", err)
		}
		// Whatever stopped it, ACME must go back to binding the port itself rather
		// than answering into a listener that is gone.
		tlsmgr.UseSharedHTTP01(false)
	}()
	log.Printf("http80: redirecting to HTTPS on %s", addr)
	return srv
}
