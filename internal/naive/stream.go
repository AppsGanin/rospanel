package naive

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// dialTimeout bounds the loopback dial to naive-in; Xray answers at once or not at all.
const dialTimeout = 5 * time.Second

// bufSize is one read of the download; the padded frames take a little more.
const bufSize = 32 << 10

var bufPool = sync.Pool{New: func() any {
	b := make([]byte, 3+bufSize+maxPadding)
	return &b
}}

// VLESS request commands.
const (
	vlessTCP = 1
	vlessUDP = 2
)

// stream is one open tunnel, tracked so SetUsers can end it. A TCP tunnel holds one
// connection to Xray, a UDP one (see uot.go) one per destination.
type stream struct {
	user User
	rc   *http.ResponseController

	mu     sync.Mutex
	conns  []net.Conn
	closed bool // ended: by SetUsers, the client going away, or the handler finishing
	cut    bool // ended by SetUsers
	done   bool // the handler has returned; rc is no longer usable
}

// attach records a connection to Xray, or closes it at once when the stream has ended.
func (st *stream) attach(c net.Conn) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		_ = c.Close()
		return false
	}
	st.conns = append(st.conns, c)
	return true
}

// detach forgets a connection the stream closed itself (a UDP destination it evicted).
func (st *stream) detach(c net.Conn) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, x := range st.conns {
		if x == c {
			st.conns = append(st.conns[:i], st.conns[i+1:]...)
			return
		}
	}
}

// end closes every connection to Xray, and with them the copies.
func (st *stream) end() {
	st.mu.Lock()
	st.closed = true
	conns := st.conns
	st.conns = nil
	st.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// close ends the tunnel of a removed user. A copy to a client that reads slowly sits in
// a write waiting for HTTP/2 flow control, which closing Xray's side does not wake — the
// expired deadline does, and serve then resets the stream instead of ending it, so the
// client drops what it has not read yet rather than playing it out for as long as its
// window holds.
//
// cut is set before anything is closed: a handler woken by the closing must already
// see it when it finishes, or it would end the stream cleanly.
func (st *stream) close() {
	st.mu.Lock()
	st.cut = true
	st.closed = true
	conns := st.conns
	st.conns = nil
	if !st.done {
		now := time.Now()
		_ = st.rc.SetWriteDeadline(now)
		_ = st.rc.SetReadDeadline(now)
	}
	st.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// writeTimeout bounds one write to the client. A client that stops granting HTTP/2 flow
// control leaves the write waiting forever, its tunnel and its place in the user's count
// held long after Xray has closed its side; past this the stream is reset.
var writeTimeout = time.Minute

// deadline sets the stream's read or write deadline (zero clears it) — unless the stream
// has ended, whose expired deadlines are what wakes the handler and must stay.
func (st *stream) deadline(write bool, at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed || st.done {
		return
	}
	if write {
		_ = st.rc.SetWriteDeadline(at)
	} else {
		_ = st.rc.SetReadDeadline(at)
	}
}

// finish marks the handler done; reports whether SetUsers cut the stream.
func (st *stream) finish() bool {
	st.end()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.done = true
	return st.cut
}

// clientWriter writes Xray's side to the client: the first frames padded when the client
// asked for it, each write flushed. Safe for concurrent use — a UDP tunnel's
// destinations answer from goroutines of their own, and a frame must go out whole.
type clientWriter struct {
	mu     sync.Mutex
	w      io.Writer
	rc     *http.ResponseController
	st     *stream
	padded bool
	frames int
	buf    []byte
}

func (cw *clientWriter) Write(p []byte) (int, error) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		out := p
		if cw.padded && cw.frames < paddedFrames {
			if cw.buf == nil {
				cw.buf = make([]byte, 3+frameData+maxPadding)
			}
			k := copy(cw.buf[3:3+frameData], p)
			out, p = padFrame(cw.buf, k), p[k:]
			cw.frames++
			if cw.frames == paddedFrames {
				defer func() { cw.buf = nil }() // the rest of the tunnel goes unpadded
			}
		} else {
			p = nil
		}
		// Armed for the write only: a deadline left standing would reset a tunnel that is
		// merely quiet, which a TCP tunnel may be for hours.
		cw.st.deadline(true, time.Now().Add(writeTimeout))
		if _, err := cw.w.Write(out); err != nil {
			return 0, err
		}
		err := cw.rc.Flush()
		cw.st.deadline(true, time.Time{})
		if err != nil {
			return 0, err
		}
	}
	return n, nil
}

// serve runs one authenticated CONNECT: the 200 goes out at once (the client may already
// be sending — NaiveProxy's "fast open"), then the stream is joined to Xray's naive-in
// until either side ends: as one VLESS TCP connection, or — for UDP over TCP — as VLESS
// UDP connections, one per destination.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, u cred) {
	host, port, uot, err := splitTarget(r.Host)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Every stream holds buffers and a goroutine before it dials anything — a UDP one
	// may never dial at all — so the user's streams are counted, not only their dials.
	if !s.reserveStream(u.Email) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer s.releaseStream(u.Email)
	padded, reply := paddingType(r.Header)
	h := w.Header()
	h.Set("Padding", paddingHeader())
	if reply != "" {
		h.Set("Padding-Type-Reply", reply)
	}
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return
	}

	st := &stream{user: u.User, rc: rc}
	s.track(st)
	defer s.untrack(st)
	// The client resetting the stream ends the tunnel too.
	stop := context.AfterFunc(r.Context(), st.end)

	var body io.Reader = r.Body
	if padded {
		body = &unpadReader{r: r.Body}
	}
	cw := &clientWriter{w: w, rc: rc, st: st, padded: padded}
	// SetUsers may have dropped the user between auth and track; it could not see
	// this stream then, so look again.
	if s.accepts(u.User) {
		if uot != uotNone {
			s.serveUDP(r, st, body, cw, u, uot)
		} else {
			s.serveTCP(r, st, body, cw, u, host, port)
		}
	}
	stop()
	// Whatever is still reading the body must be woken before the handler returns.
	_ = r.Body.Close()
	if st.finish() {
		panic(http.ErrAbortHandler) // RST_STREAM: the user was removed
	}
}

// serveTCP joins the stream to one VLESS TCP connection to host:port.
func (s *Server) serveTCP(r *http.Request, st *stream, body io.Reader, cw *clientWriter, u cred, host string, port uint16) {
	conn, err := s.dialXray(r, st, u, vlessTCP, host, port)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		bp := bufPool.Get().(*[]byte)
		defer bufPool.Put(bp)
		// Writer-only, so the copy uses the pooled buffer: *net.TCPConn's ReadFrom would
		// allocate one of its own for every tunnel.
		_, err := io.CopyBuffer(struct{ io.Writer }{conn}, body, (*bp)[:bufSize])
		if err != nil {
			_ = conn.Close()
			return
		}
		// The client is done sending; the reply may still be coming.
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	download(cw, conn)
	_ = conn.Close()
	// The upload must be finished with the body before the handler returns; closing it
	// is what wakes a read that is waiting on the client.
	_ = r.Body.Close()
	<-done
}

// maxConnsPerUser bounds the connections to Xray one user holds through the lane at
// once. A NaiveProxy client multiplexes many tunnels over one TLS connection, and a UDP
// tunnel opens one connection per destination, so without it one connection could make
// the box open thousands. A browser stays far below; past it a TCP tunnel ends and a UDP
// packet to a new destination is dropped.
const maxConnsPerUser = 1024

// errTooManyConns is a dial refused by maxConnsPerUser.
var errTooManyConns = errors.New("naive: too many open tunnels for this user")

// dialXray opens a VLESS connection on naive-in for the stream: the PROXY line naming
// the client, then the request header. The stream owns it from here.
func (s *Server) dialXray(r *http.Request, st *stream, u cred, cmd byte, host string, port uint16) (net.Conn, error) {
	if !s.reserve(u.Email) {
		return nil, errTooManyConns
	}
	ctx, cancel := context.WithTimeout(r.Context(), dialTimeout)
	inner := *s.inner.Load()
	raw, err := (&net.Dialer{}).DialContext(ctx, innerNetwork(inner), inner)
	cancel()
	if err != nil {
		s.release(u.Email)
		return nil, err
	}
	conn := &xrayConn{Conn: raw, release: func() { s.release(u.Email) }}
	if !st.attach(conn) {
		return nil, net.ErrClosed
	}
	req := proxyHeader(r.RemoteAddr)
	req = appendVLESSRequest(req, u.id, cmd, host, port)
	if _, err := conn.Write(req); err != nil {
		st.detach(conn)
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// xrayConn is a connection to naive-in that gives its place in the user's count back
// once, on the first Close.
type xrayConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *xrayConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// CloseWrite half-closes: the client is done sending, the reply may still be coming.
func (c *xrayConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// maxStreamsPerUser bounds the tunnels one user holds open at once. A browser behind
// NaiveProxy keeps well under a hundred; this is there for a client that opens streams
// and sends nothing.
var maxStreamsPerUser = 512

// reserveStream takes a place in a user's count of open streams, if one is free.
func (s *Server) reserveStream(email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userStreams[email] >= maxStreamsPerUser {
		return false
	}
	s.userStreams[email]++
	return true
}

func (s *Server) releaseStream(email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userStreams[email] <= 1 {
		delete(s.userStreams, email)
		return
	}
	s.userStreams[email]--
}

// reserve takes a place in a user's count of open connections, if one is free.
func (s *Server) reserve(email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perUser[email] >= maxConnsPerUser {
		return false
	}
	s.perUser[email]++
	return true
}

func (s *Server) release(email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perUser[email] <= 1 {
		delete(s.perUser, email)
		return
	}
	s.perUser[email]--
}

// download copies Xray's side to the client.
func download(cw io.Writer, conn net.Conn) {
	if !readVLESSResponse(conn) {
		return
	}
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	buf := (*bp)[:bufSize]
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if _, werr := cw.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// accepts reports whether u is still among the current credentials.
func (s *Server) accepts(u User) bool {
	m := s.users.Load()
	if m == nil {
		return false
	}
	c, ok := (*m)[u.UUID]
	return ok && c.Email == u.Email
}

// splitTarget parses a CONNECT authority, host:port, and recognises the addresses that
// ask for UDP over TCP instead of a TCP connection.
func splitTarget(authority string) (string, uint16, uotVersion, error) {
	host, p, err := net.SplitHostPort(authority)
	if err != nil {
		return "", 0, uotNone, err
	}
	switch host {
	case uotMagic:
		return host, 0, uotV2, nil
	case uotMagicLegacy:
		return host, 0, uotV1, nil
	}
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil || port == 0 {
		return "", 0, uotNone, fmt.Errorf("bad port %q", p)
	}
	if host == "" || len(host) > 255 {
		return "", 0, uotNone, fmt.Errorf("bad host %q", host)
	}
	return host, uint16(port), uotNone, nil
}

// parseUUID turns the textual UUID into the 16 bytes VLESS sends.
func parseUUID(s string) ([16]byte, bool) {
	var id [16]byte
	h := strings.ReplaceAll(s, "-", "")
	if len(h) != 32 {
		return id, false
	}
	if _, err := hex.Decode(id[:], []byte(h)); err != nil {
		return id, false
	}
	return id, true
}

// appendVLESSRequest appends a VLESS request header to host:port: version 0, the user's
// id, no addons (naive-in's clients carry no flow), the command.
func appendVLESSRequest(b []byte, id [16]byte, cmd byte, host string, port uint16) []byte {
	b = append(b, 0)
	b = append(b, id[:]...)
	b = append(b, 0, cmd)
	b = binary.BigEndian.AppendUint16(b, port)
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Is4() || ip.Is4In6() {
			a := ip.Unmap().As4()
			return append(append(b, 1), a[:]...)
		}
		a := ip.As16()
		return append(append(b, 3), a[:]...)
	}
	b = append(b, 2, byte(len(host)))
	return append(b, host...)
}

// readVLESSResponse consumes the VLESS response header: version, addons length, addons.
func readVLESSResponse(r io.Reader) bool {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil || hdr[0] != 0 {
		return false
	}
	if hdr[1] > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(hdr[1])); err != nil {
			return false
		}
	}
	return true
}

// proxyHeader is a PROXY v1 line naming the client, so naive-in logs — and the access
// log's consumers act on — the address the tunnel really came from. The destination is
// a placeholder of the same family: Xray reads only the source.
func proxyHeader(remote string) []byte {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return []byte("PROXY UNKNOWN\r\n") // no address to name; Xray keeps the socket's
	}
	ip := ap.Addr().Unmap().WithZone("") // a zone is no part of the PROXY grammar
	if ip.Is4() {
		return fmt.Appendf(nil, "PROXY TCP4 %s 127.0.0.1 %d 443\r\n", ip, ap.Port())
	}
	return fmt.Appendf(nil, "PROXY TCP6 %s ::1 %d 443\r\n", ip, ap.Port())
}
