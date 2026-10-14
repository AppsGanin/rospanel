package decoy

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// HTTP/3 on the same port.
//
// Hysteria2 listens on UDP where the decoy listens on TCP, usually both on 443, and
// answers an HTTP/3 request that is not a Hysteria2 client by Xray's masquerade. Left
// at its default that is Go's bare "404 page not found" with no Server header — next
// to a whole Caddy site over TCP, the one-request tell of a Hysteria2 server. So the
// masquerade proxies to ServeLoopback, which serves this same decoy; and the decoy
// advertises HTTP/3 the way Caddy does, since a Caddy whose UDP/443 answers QUIC and
// that never says so is a contradiction too.

// altSvc is the Alt-Svc value every decoy response carries; nil sends none.
var altSvc atomic.Pointer[string]

// SetAltSvc advertises HTTP/3 on port, as Caddy does; 0 stops advertising it.
func SetAltSvc(port int) {
	if port <= 0 {
		altSvc.Store(nil)
		return
	}
	v := fmt.Sprintf(`h3=":%d"; ma=2592000`, port)
	altSvc.Store(&v)
}

// ServeLoopback serves h on addr until the process exits — the target of Hysteria2's
// masquerade (xray.DecoyH3Addr). It serves nothing but the decoy: every request
// reaches it from 127.0.0.1, so the panel behind the real listener must not be here.
func ServeLoopback(addr string, h http.Handler) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Warn("decoy: HTTP/3 masquerade listener", "addr", addr, "err", err)
		return
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
}
