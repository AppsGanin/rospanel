package decoy

import (
	"log/slog"
	"net"
	"net/http"
	"time"
)

// HTTP/3 on the same port.
//
// Hysteria2 listens on UDP where the decoy listens on TCP, usually both on 443, and
// answers an HTTP/3 request that is not a Hysteria2 client by Xray's masquerade. Left
// at its default that is Go's bare "404 page not found" with no Server header — next
// to a whole Caddy site over TCP, the one-request tell of a Hysteria2 server. So the
// masquerade proxies to ServeLoopback, which serves this same decoy.
//
// The decoy does NOT advertise that with Alt-Svc, and must not. The header is scoped to
// the origin, not to a path: a browser that loaded the decoy once — the site root, a
// mistyped link, a 404 — then sends every later request for that origin over HTTP/3,
// the hidden panel and /sub/ links included, and those come back as the decoy because
// the masquerade serves nothing else (it cannot: every request reaches it from
// 127.0.0.1, see ServeLoopback). That is a panel bricked for up to the header's
// max-age, in whichever browser happened to look at the front page. A Caddy that
// answers QUIC but never says so is an ordinary Caddy (protocols h1 h2), and either
// way only an active probe of UDP/443 can tell the difference.

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
