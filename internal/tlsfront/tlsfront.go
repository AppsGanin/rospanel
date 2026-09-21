// Package tlsfront listens on the TCP-TLS lane's public port in front of Xray.
//
// Xray used to own that port, and a client that sent it plain HTTP instead of a TLS
// ClientHello had the connection closed without a word. That gives Xray away: the
// decoy behind the port says it is Caddy, and Caddy — any Go HTTPS server — answers
// plain HTTP on its TLS port with a 400. One "GET / HTTP/1.1" told the two apart.
//
// The front reads the first five bytes of each connection. A request line that Go's
// HTTPS server would take for plain HTTP gets the reply net/http writes, word for word;
// everything else goes to Xray on loopback untouched, behind a PROXY header, so Xray —
// its access log, the device limits, the decoy behind its fallback — still sees the
// client's own address. Anything else Xray used to close silently it still closes the
// same way, because it is still Xray that reads it.
package tlsfront

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"
)

const (
	// firstBytesTimeout bounds the wait for the record header: Xray's own handshake
	// timeout (policy "handshake", 60 s unless the config sets it, which the panel's
	// does not). A client that connects and says nothing was closed by Xray after that
	// long, and a shorter wait here would be one more thing a probe could tell apart.
	firstBytesTimeout = 60 * time.Second
	dialTimeout       = 5 * time.Second
	// firstReadMax is how much of the first flight is read before deciding: a
	// ClientHello, or a whole request line and headers.
	firstReadMax     = 4096
	retryListenEvery = time.Second
)

// badRequest is what net/http writes on a TLS connection whose first record looks like
// plain HTTP (net/http.(*conn).serve).
const badRequest = "HTTP/1.0 400 Bad Request\r\n\r\nClient sent an HTTP request to an HTTPS server.\n"

// looksLikeHTTP is net/http's tlsRecordHeaderLooksLikeHTTP: the only first five bytes
// Go's HTTPS server answers instead of closing.
func looksLikeHTTP(hdr [5]byte) bool {
	switch string(hdr[:]) {
	case "GET /", "HEAD ", "POST ", "PUT /", "OPTIO":
		return true
	}
	return false
}

// Front hands one public port's connections to Xray on loopback.
type Front struct {
	target string // Xray's loopback address

	mu   sync.Mutex
	port int
	ln   net.Listener
	stop chan struct{} // closed to end the current port's listener
}

// New returns a front for Xray at target; it listens nowhere until Ensure.
func New(target string) *Front { return &Front{target: target} }

// Ensure keeps the front on port, all addresses, IPv4 and IPv6 — as Xray listened; 0
// stops it.
// A port that cannot be taken yet (the Xray that held it is still going away) is tried
// again every second until it can be, or until Ensure asks for something else.
func (f *Front) Ensure(port int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if port == f.port {
		return
	}
	f.stopLocked()
	if port == 0 {
		return
	}
	f.port = port
	stop := make(chan struct{})
	f.stop = stop
	go f.run(port, stop)
}

// Stop closes the listener. Connections already handed to Xray go on.
func (f *Front) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopLocked()
}

// Port is the port the front is on (or trying to be on); 0 when stopped.
func (f *Front) Port() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.port
}

func (f *Front) stopLocked() {
	if f.stop != nil {
		close(f.stop)
		f.stop = nil
	}
	if f.ln != nil {
		_ = f.ln.Close()
		f.ln = nil
	}
	f.port = 0
}

func (f *Front) run(port int, stop chan struct{}) {
	// Dual-stack, the socket Xray's "listen": "0.0.0.0" gave: Go opens that as [::].
	addr := fmt.Sprintf(":%d", port)
	var ln net.Listener
	for warned := false; ; {
		l, err := net.Listen("tcp", addr)
		if err == nil {
			ln = l
			break
		}
		if !warned {
			slog.Warn("tlsfront: port not free yet, retrying", "addr", addr, "err", err)
			warned = true
		}
		select {
		case <-stop:
			return
		case <-time.After(retryListenEvery):
		}
	}
	f.mu.Lock()
	if f.stop != stop { // stopped or moved while this was waiting for the port
		f.mu.Unlock()
		_ = ln.Close()
		return
	}
	f.ln = ln
	f.mu.Unlock()
	slog.Info("tlsfront: listening", "addr", addr, "xray", f.target)
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond): // out of descriptors and the like
			}
			continue
		}
		go f.handle(c)
	}
}

func (f *Front) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(firstBytesTimeout))
	// Whatever has arrived, at least a record header — the way crypto/tls reads it. Read
	// to the byte, a request that came in one packet would be left half unread, and the
	// close below would go out as a reset where Go's server sends a clean FIN.
	buf := make([]byte, firstReadMax)
	n := 0
	for n < 5 {
		m, err := c.Read(buf[n:])
		n += m
		if err != nil && n < 5 {
			return
		}
	}
	if looksLikeHTTP([5]byte(buf[:5])) {
		_ = c.SetWriteDeadline(time.Now().Add(firstBytesTimeout))
		_, _ = io.WriteString(c, badRequest)
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	up, err := net.DialTimeout("tcp", f.target, dialTimeout)
	if err != nil {
		// Xray is not there (restarting, crashed): reset, the nearest a connection
		// already accepted can come to the refusal a port nobody listens on gives.
		reset(c)
		return
	}
	defer up.Close()
	head, err := proxyHeader(c.RemoteAddr(), c.LocalAddr())
	if err != nil {
		return
	}
	if _, err := up.Write(append(head, buf[:n]...)); err != nil {
		return
	}
	pipe(c, up)
}

// proxyHeader is a PROXY protocol v1 line for a connection from src to dst.
func proxyHeader(src, dst net.Addr) ([]byte, error) {
	s, ok1 := src.(*net.TCPAddr)
	d, ok2 := dst.(*net.TCPAddr)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("not a TCP connection")
	}
	family := "TCP6"
	sip, dip := s.IP, d.IP
	if s4, d4 := sip.To4(), dip.To4(); s4 != nil && d4 != nil {
		family, sip, dip = "TCP4", s4, d4
	}
	return fmt.Appendf(nil, "PROXY %s %s %s %d %d\r\n", family, sip, dip, s.Port, d.Port), nil
}

// pipe copies both ways until both are done, so the client sees Xray's end of the
// connection as it would have from Xray itself:
//   - the client finishing its upload (FIN) reaches Xray as a half-close — a VPN client
//     that is done sending still has a download coming;
//   - Xray closing ends the client's connection whole — it closes, it does not half-close,
//     and a client that never answers must not keep this pair open;
//   - Xray resetting (a probe's bytes it left unread, say) resets the client;
//   - a connection the panel closed under a removed user (see xray/conncut.go) fails the
//     client side, which takes Xray's side down with it.
func pipe(client, xray net.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := io.Copy(xray, client)
		if err != nil {
			_ = client.Close()
			_ = xray.Close()
			return
		}
		if cw, ok := xray.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	_, err := io.Copy(client, xray)
	if err != nil && isReset(err) {
		reset(client)
	}
	_ = client.Close()
	_ = xray.Close()
	<-done
}

// reset closes c with a RST rather than a FIN.
func reset(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = c.Close()
}

func isReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET)
}
