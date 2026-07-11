// Package proxyproto provides a net.Listener that reads an optional
// PROXY-protocol v1 header (the text format Xray emits with xver=1) from each
// accepted connection and exposes the real client address via RemoteAddr.
//
// The panel's HTTP server sits behind Xray, which terminates TLS and forwards
// the decrypted request over loopback — so without this the panel only ever
// sees 127.0.0.1. Connections that arrive WITHOUT a PROXY header (anything not
// routed through the xver fallback) pass through unchanged, keeping their real
// transport address, so the wrapper is safe to install unconditionally.
package proxyproto

import (
	"bufio"
	"bytes"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// headerTimeout bounds how long we wait for the PROXY header bytes before giving
// up and treating the connection as header-less (keeps a silent peer from
// stalling the connection's serve goroutine).
const headerTimeout = 5 * time.Second

// Listener wraps a net.Listener, parsing a PROXY v1 header off each connection.
type Listener struct{ net.Listener }

// Accept returns a connection that lazily parses its PROXY header on first use.
func (l *Listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &conn{Conn: c, r: bufio.NewReader(c)}, nil
}

// conn overrides Read and RemoteAddr; everything else delegates to net.Conn.
type conn struct {
	net.Conn
	r      *bufio.Reader
	once   sync.Once
	remote net.Addr // real client addr from the PROXY header, nil if none
}

func (c *conn) Read(p []byte) (int, error) {
	c.parse()
	return c.r.Read(p)
}

// RemoteAddr returns the client address from the PROXY header when present,
// else the underlying transport address. net/http reads this before the first
// Read, so parsing is also triggered here.
func (c *conn) RemoteAddr() net.Addr {
	c.parse()
	if c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

// parse consumes a leading PROXY v1 header exactly once. When the connection
// doesn't start with the "PROXY " signature the buffered bytes are left intact
// for Read and the real transport address is kept.
func (c *conn) parse() {
	c.once.Do(func() {
		// Only a loopback peer may declare a client IP via the PROXY header — the
		// panel's sole legitimate upstream is Xray forwarding over 127.0.0.1. Honoring
		// the header from any other peer would let a direct (non-Xray) connection spoof
		// its source IP and so reset/evade the per-IP login throttle and poison audit
		// logs. A non-loopback peer keeps its real transport address.
		if !isLoopback(c.Conn.RemoteAddr()) {
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(headerTimeout))
		defer func() { _ = c.SetReadDeadline(time.Time{}) }()

		sig, err := c.r.Peek(6)
		if err != nil || !bytes.Equal(sig, []byte("PROXY ")) {
			return // no header → keep buffered bytes + real addr
		}
		line, err := c.r.ReadString('\n') // consume "PROXY ...\r\n"
		if err != nil {
			return
		}
		c.remote = parseV1(line)
	})
}

// isLoopback reports whether a is a loopback address (127.0.0.0/8 or ::1).
func isLoopback(a net.Addr) bool {
	if a == nil {
		return false
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		host = a.String()
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// parseV1 parses a PROXY v1 header line, e.g.
//
//	PROXY TCP4 1.2.3.4 5.6.7.8 56324 443
//
// Returns nil for "PROXY UNKNOWN" or any malformed line (caller falls back to
// the transport address).
func parseV1(line string) net.Addr {
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) < 6 || f[0] != "PROXY" {
		return nil
	}
	ip := net.ParseIP(f[2]) // source address
	if ip == nil {
		return nil
	}
	port, err := strconv.Atoi(f[4]) // source port
	if err != nil {
		return nil
	}
	return &net.TCPAddr{IP: ip, Port: port}
}
