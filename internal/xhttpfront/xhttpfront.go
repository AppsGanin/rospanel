// Package xhttpfront puts the VLESS-XHTTP-TLS lane on the TCP-TLS lane's port, behind
// the decoy, on a path of its own.
//
// That port is one listener for many things: Xray's VLESS-TLS inbound takes what it
// can read as VLESS and hands everything else, decrypted, to the panel (or a node's
// decoy) through its fallback — the decoy site, the panel itself, NaiveProxy,
// TrustTunnel, AnyTLS. XHTTP is HTTP, so it arrives there too. Xray's own fallbacks can
// pick a destination by path only for HTTP/1.1, while an XHTTP client speaks HTTP/2 to
// a TLS server, so the split by path is made here, at the HTTP layer: a request on one
// of the routed paths is relayed to that inbound's inner socket, everything else goes on
// to the decoy as before.
//
// Each client connection gets its own upstream connection, opened with a PROXY line
// naming the client, so Xray sees every user from their own address — for the access
// log, the device limit, and the cut that closes a removed user's sockets on the public
// port (xray/conncut.go). The forwarding headers a client could forge are dropped, and
// the inbound is told to trust none.
package xhttpfront

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AppsGanin/rospanel/internal/decoy"
	"github.com/AppsGanin/rospanel/internal/vlessin"
)

const (
	dialTimeout = 5 * time.Second
	// idleTimeout closes an upstream connection whose client connection is gone; the
	// next request from the same client, if any, opens another.
	idleTimeout = 30 * time.Second
)

// Route sends requests under Path ("/name", no trailing slash) to the XHTTP inbound at
// Addr — an abstract unix socket ("@name") or a loopback "host:port".
type Route struct {
	Path string
	Addr string
}

// Router relays the routed paths. The zero value is not usable; call New.
type Router struct {
	routes atomic.Pointer[[]Route]
	proxy  *httputil.ReverseProxy
	tr     *http.Transport
}

// New returns a router with no routes: every request goes on to next.
func New() *Router {
	r := &Router{}
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true) // Xray's XHTTP server speaks h2c on a plain listener
	r.tr = &http.Transport{
		Protocols:           protocols,
		DialContext:         dialUpstream,
		IdleConnTimeout:     idleTimeout,
		MaxIdleConnsPerHost: 1,
		ForceAttemptHTTP2:   true,
	}
	r.proxy = &httputil.ReverseProxy{
		Rewrite:        rewrite,
		Transport:      r.tr,
		FlushInterval:  -1, // XHTTP downloads are streams
		ModifyResponse: modifyResponse,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set("Server", decoy.ServerName)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return r
}

// SetRoutes replaces the routed paths. Nil-safe.
func (r *Router) SetRoutes(routes []Route) {
	if r == nil {
		return
	}
	cp := make([]Route, 0, len(routes))
	for _, rt := range routes {
		p := strings.TrimRight(rt.Path, "/")
		if p == "" || rt.Addr == "" {
			continue // never "/": that would take the whole site
		}
		cp = append(cp, Route{Path: p, Addr: rt.Addr})
	}
	// Called on every config applied, users coming and going included: the connections
	// to the inbounds are dropped only when where they lead has changed.
	if old := r.routes.Swap(&cp); old == nil || !slices.Equal(*old, cp) {
		r.tr.CloseIdleConnections()
	}
}

// Routes is the current set, for tests and logs.
func (r *Router) Routes() []Route {
	if p := r.routes.Load(); p != nil {
		return *p
	}
	return nil
}

// match is the inbound for a request path, or "".
func (r *Router) match(path string) string {
	p := r.routes.Load()
	if p == nil {
		return ""
	}
	for _, rt := range *p {
		if path == rt.Path || strings.HasPrefix(path, rt.Path+"/") {
			return rt.Addr
		}
	}
	return ""
}

// Wrap relays the routed paths and hands every other request to next. Nil-safe.
func (r *Router) Wrap(next http.Handler) http.Handler {
	if r == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		addr := r.match(req.URL.Path)
		if addr == "" {
			next.ServeHTTP(w, req)
			return
		}
		// A tunnel outlives any deadline the server set for ordinary requests, and an
		// HTTP/1.1 upload streams while its download does.
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Time{})
		_ = rc.SetWriteDeadline(time.Time{})
		_ = rc.EnableFullDuplex()
		req = req.WithContext(context.WithValue(req.Context(), upstreamKey{}, upstreamHost(addr, req.RemoteAddr)))
		r.proxy.ServeHTTP(w, req)
	})
}

type upstreamKey struct{}

// upstreamHost names the pool entry for one client connection to one inbound: the
// transport keeps one connection per host, so a host per (inbound, client address)
// gives every client its own upstream connection, with its own PROXY line.
func upstreamHost(addr, remote string) string {
	return "x" + hex.EncodeToString([]byte(addr+"|"+remote)) + ".invalid"
}

// parseUpstreamHost undoes upstreamHost.
func parseUpstreamHost(host string) (addr, remote string, ok bool) {
	h, found := strings.CutPrefix(host, "x")
	if !found {
		return "", "", false
	}
	h, found = strings.CutSuffix(h, ".invalid")
	if !found {
		return "", "", false
	}
	raw, err := hex.DecodeString(h)
	if err != nil {
		return "", "", false
	}
	addr, remote, ok = strings.Cut(string(raw), "|")
	return addr, remote, ok && addr != ""
}

func rewrite(pr *httputil.ProxyRequest) {
	host, _ := pr.In.Context().Value(upstreamKey{}).(string)
	pr.Out.URL.Scheme = "http"
	pr.Out.URL.Host = host
	pr.Out.Host = pr.In.Host // the inbound may check the Host the client sent
	// Rewrite already leaves out Forwarded and X-Forwarded-*; a client's own claim to
	// another address goes too, whatever the inbound would make of it.
	pr.Out.Header.Del("X-Real-Ip")
	pr.Out.Header.Del("X-Forwarded-For")
	pr.Out.Header.Del("Forwarded")
}

func modifyResponse(resp *http.Response) error {
	resp.Header.Set("Server", decoy.ServerName) // the same server as the rest of the site
	return nil
}

// dialUpstream opens the inbound named in the pool host and writes the PROXY line for
// the client the connection serves.
func dialUpstream(ctx context.Context, _, hostport string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	addr, remote, ok := parseUpstreamHost(host)
	if !ok {
		return nil, fmt.Errorf("xhttpfront: bad upstream %q", hostport)
	}
	d := net.Dialer{Timeout: dialTimeout}
	c, err := d.DialContext(ctx, vlessin.Network(addr), addr)
	if err != nil {
		return nil, err
	}
	if _, err := c.Write(vlessin.ProxyHeader(remote)); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
