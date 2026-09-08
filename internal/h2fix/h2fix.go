// Package h2fix keeps long-lived plaintext HTTP/2 responses alive.
//
// net/http implements Server.ReadHeaderTimeout as a read deadline set on the raw
// connection before the protocol is known (conn.serve). On the unencrypted-HTTP/2
// path the connection is then handed straight to the HTTP/2 server, which disarms
// that deadline only when Server.ReadTimeout is set — and a server that streams
// must leave ReadTimeout unset, or every held response dies at ReadTimeout instead.
// So the deadline stays armed for the life of the connection: an unencrypted h2
// connection is torn down ReadHeaderTimeout after it opened, whatever is flowing
// through it.
//
// The panel walks into this head-on. It sits behind Xray's :443 VLESS fallback,
// whose TLS offers ALPN h2, so every browser reaches the panel over plaintext h2 —
// and the dashboard holds an SSE stream open. The symptom is a stream that dies
// every ReadHeaderTimeout with net::ERR_CONNECTION_CLOSED and a dashboard that
// blinks "connection lost — reconnecting" forever, while short requests look fine
// because the browser silently retries them on a fresh connection. The node agent
// hit the same wall from the other side and sidesteps it by forcing HTTP/1.1 (see
// nodeagent.syncTransport); a browser cannot be told to do that.
//
// Listener disarms the deadline for the one case net/http forgets, and leaves
// HTTP/1.1 connections alone — there the header timeout works as intended, and
// net/http clears it itself once the headers are in. An h2 connection that goes
// quiet is still reaped: Server.IdleTimeout is honoured by the HTTP/2 server.
package h2fix

import (
	"net"
	"time"
)

// clientPreface is what an HTTP/2 client sends first, on TLS and in the clear
// alike (RFC 9113 §3.4). net/http sniffs the same bytes to decide whether to hand
// the connection to the HTTP/2 server.
const clientPreface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

// Listener wraps a listener so connections that turn out to speak HTTP/2 lose the
// header-read deadline. Wrap it around whatever listener the server would serve —
// including another wrapper, such as the PROXY-protocol one.
type Listener struct {
	net.Listener
}

func (l Listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return c, err
	}
	return &conn{Conn: c}, nil
}

// conn watches the first bytes a client sends for the HTTP/2 preface. Matching is
// incremental because the preface can be split across reads, and stops as soon as
// the answer is known either way — after that this is a plain net.Conn.
type conn struct {
	net.Conn
	matched int // bytes of the preface seen so far, or -1 once decided
}

func (c *conn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.matched >= 0 && n > 0 {
		c.sniff(p[:n])
	}
	return n, err
}

func (c *conn) sniff(b []byte) {
	for _, got := range b {
		if clientPreface[c.matched] != got {
			c.matched = -1 // HTTP/1.1: net/http owns the deadline from here
			return
		}
		c.matched++
		if c.matched == len(clientPreface) {
			c.matched = -1
			// The whole preface arrived in time, so the header timeout has done its
			// job. net/http is about to hand this connection to the HTTP/2 server
			// without disarming it; do that here instead.
			_ = c.SetReadDeadline(time.Time{})
			return
		}
	}
}
