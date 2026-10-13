package mieru

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	mieruconst "github.com/enfein/mieru/v3/apis/constant"
	"github.com/enfein/mieru/v3/apis/model"

	"github.com/AppsGanin/rospanel/internal/vlessin"
)

const (
	// requestTimeout bounds how long a new session may take to say what it wants.
	requestTimeout = 10 * time.Second
	dialTimeout    = 5 * time.Second
	// maxConnsPerUser bounds a user's open connections to Xray, across sessions.
	maxConnsPerUser = 1024
	// maxUDPDests bounds the destinations one UDP session keeps a connection to Xray
	// for; past it the one quiet longest is closed.
	maxUDPDests = 64
)

// maxSessionsPerUser bounds the sessions one user holds open at once.
var maxSessionsPerUser = 512

// udpIdle ends a UDP session the client has sent nothing on for this long.
var udpIdle = 5 * time.Minute

var errTooManyConns = errors.New("mieru: too many open tunnels for this user")

// session is one mieru session, tracked so Apply can end it. A TCP session holds one
// connection to Xray, a UDP one one per destination.
type session struct {
	user User
	conn net.Conn // the mieru session

	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

// attach records a connection to Xray, or closes it at once when the session has ended.
func (ss *session) attach(c net.Conn) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		_ = c.Close()
		return false
	}
	ss.conns = append(ss.conns, c)
	return true
}

func (ss *session) detach(c net.Conn) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for i, x := range ss.conns {
		if x == c {
			ss.conns = append(ss.conns[:i], ss.conns[i+1:]...)
			return
		}
	}
}

// close ends the session: the client's side and every connection to Xray.
func (ss *session) close() {
	ss.mu.Lock()
	if ss.closed {
		ss.mu.Unlock()
		return
	}
	ss.closed = true
	conns := ss.conns
	ss.conns = nil
	ss.mu.Unlock()
	// Xray's side first: it closes at once, and stops the traffic; mieru's own close
	// may wait for what the session still has queued.
	for _, c := range conns {
		_ = c.Close()
	}
	_ = ss.conn.Close()
}

// handle serves one session: the SOCKS5 request mieru carries, then the tunnel.
func (s *Server) handle(c net.Conn) {
	// The request first: mieru names a session's user once it has decrypted the
	// session's data, which a session just accepted may not have yet. Bounded by a
	// timer, not a read deadline: mieru clears a session's deadline after every read,
	// and the request takes several.
	expired := time.AfterFunc(requestTimeout, func() { _ = c.Close() })
	var req model.Request
	err := req.ReadFromSocks5(c)
	if !expired.Stop() || err != nil {
		_ = c.Close()
		return
	}
	u, ok := s.lookup(c)
	if !ok || !s.raw.admit(c.RemoteAddr().String(), u.Email) {
		_ = c.Close()
		return
	}
	ss := &session{user: u.User, conn: c}
	if !s.track(ss) {
		_ = c.Close()
		return
	}
	defer s.untrack(ss)
	defer ss.close()
	// Apply may have dropped the user between the lookup and track; it could not see
	// the session then.
	if !s.accepts(u.User) {
		return
	}
	switch req.Command {
	case mieruconst.Socks5ConnectCmd:
		s.serveTCP(ss, u, req.DstAddr)
	case mieruconst.Socks5UDPAssociateCmd:
		s.serveUDP(ss, u)
	default:
		_ = reply(c, mieruconst.Socks5ReplyCommandNotSupported)
	}
}

// reply answers the SOCKS5 request.
func reply(c net.Conn, code byte) error {
	r := model.Response{Reply: code, BindAddr: model.AddrSpec{IP: net.IPv4zero}}
	return r.WriteToSocks5(c)
}

// target is the request's destination as VLESS addresses it.
func target(a model.AddrSpec) (string, uint16, bool) {
	if a.Port <= 0 || a.Port > 65535 {
		return "", 0, false
	}
	if a.FQDN != "" {
		if len(a.FQDN) > 255 {
			return "", 0, false
		}
		return a.FQDN, uint16(a.Port), true
	}
	ip, ok := netip.AddrFromSlice(a.IP)
	if !ok {
		return "", 0, false
	}
	return ip.Unmap().String(), uint16(a.Port), true
}

// serveTCP carries a CONNECT through one VLESS connection.
func (s *Server) serveTCP(ss *session, u cred, dst model.AddrSpec) {
	host, port, ok := target(dst)
	if !ok {
		_ = reply(ss.conn, mieruconst.Socks5ReplyAddrTypeNotSupported)
		return
	}
	xc, err := s.dialXray(ss, u, vlessin.CmdTCP, host, port)
	if err != nil {
		_ = reply(ss.conn, mieruconst.Socks5ReplyServerFailure)
		return
	}
	if err := reply(ss.conn, mieruconst.Socks5ReplySuccess); err != nil {
		return
	}
	up := make(chan struct{})
	go func() {
		defer close(up)
		_, _ = io.Copy(xc, ss.conn)
		if cw, ok := xc.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	if vlessin.ReadResponse(xc) {
		_, _ = io.Copy(ss.conn, xc)
	}
	ss.close() // Xray is done: so is the session, and with it the upload
	<-up
}

// dialXray opens a VLESS connection on mieru-in for the session: the PROXY line naming
// the client, then the request header. The session owns it from here.
func (s *Server) dialXray(ss *session, u cred, cmd byte, host string, port uint16) (net.Conn, error) {
	if !s.reserve(u.Email) {
		return nil, errTooManyConns
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	inner := *s.inner.Load()
	raw, err := (&net.Dialer{}).DialContext(ctx, vlessin.Network(inner), inner)
	cancel()
	if err != nil {
		s.release(u.Email)
		return nil, err
	}
	conn := &xrayConn{Conn: raw, release: func() { s.release(u.Email) }}
	if !ss.attach(conn) {
		return nil, net.ErrClosed
	}
	req := vlessin.ProxyHeader(ss.conn.RemoteAddr().String())
	req = vlessin.AppendRequest(req, u.id, cmd, host, port)
	if _, err := conn.Write(req); err != nil {
		ss.detach(conn)
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// xrayConn is a connection to mieru-in that gives its place in the user's count back
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

// udpDest is one destination of a UDP session.
type udpDest struct {
	conn net.Conn
	addr []byte      // the address as the client wrote it (ATYP, address, port)
	used uint64      // when it last sent, in packets of the session
	dead atomic.Bool // Xray ended the connection (idle timeout, a failed outbound)
}

// serveUDP carries a UDP ASSOCIATE: SOCKS5 UDP datagrams, each framed by mieru as
// 0x00, length, datagram, 0xff, demultiplexed onto one VLESS UDP connection per
// destination; replies come back the same way, addressed from that destination.
func (s *Server) serveUDP(ss *session, u cred) {
	if err := reply(ss.conn, mieruconst.Socks5ReplySuccess); err != nil {
		return
	}
	w := &frameWriter{conn: ss.conn}
	br := bufio.NewReaderSize(ss.conn, 4<<10)
	dests := map[string]*udpDest{}
	var wg sync.WaitGroup
	// A client quiet for udpIdle ends the session — a timer rather than a read deadline,
	// which mieru clears after every read.
	idle := time.AfterFunc(udpIdle, ss.close)
	defer func() {
		idle.Stop()
		// The session first: a reply blocked writing to a client that stopped reading
		// is woken only by its closing.
		ss.close()
		wg.Wait()
	}()
	frame := make([]byte, 1500) // grows to the largest datagram seen
	out := make([]byte, 0, 2+1500)
	var seq uint64
	for {
		n, err := readFrame(br, &frame)
		idle.Reset(udpIdle)
		if err != nil {
			return
		}
		raw, host, port, payload, ok := parseDatagram(frame[:n])
		if !ok {
			continue // a fragment, or garbage: dropped, as UDP may drop it
		}
		key := net.JoinHostPort(host, strconv.Itoa(int(port)))
		d := dests[key]
		if d != nil && d.dead.Load() {
			// Xray let this destination go; a write into the closed connection would
			// lose the packet without an error. Open it again instead.
			_ = d.conn.Close()
			ss.detach(d.conn)
			delete(dests, key)
			d = nil
		}
		if d == nil {
			if len(dests) >= maxUDPDests {
				evictIdlest(ss, dests)
			}
			conn, err := s.dialXray(ss, u, vlessin.CmdUDP, host, port)
			if errors.Is(err, net.ErrClosed) {
				return // the session has ended
			}
			if err != nil {
				continue
			}
			d = &udpDest{conn: conn, addr: append([]byte(nil), raw...)}
			dests[key] = d
			wg.Add(1)
			go func() {
				defer wg.Done()
				udpReplies(d, w)
			}()
		}
		seq++
		d.used = seq
		out = binary.BigEndian.AppendUint16(out[:0], uint16(len(payload)))
		out = append(out, payload...)
		if _, err := d.conn.Write(out); err != nil {
			_ = d.conn.Close()
			ss.detach(d.conn)
			delete(dests, key)
		}
	}
}

// evictIdlest closes the destination that has gone longest without sending.
func evictIdlest(ss *session, dests map[string]*udpDest) {
	var key string
	var idlest *udpDest
	for k, d := range dests {
		if idlest == nil || d.used < idlest.used {
			key, idlest = k, d
		}
	}
	if idlest != nil {
		_ = idlest.conn.Close()
		ss.detach(idlest.conn)
		delete(dests, key)
	}
}

// udpReplies copies one destination's packets back to the client.
func udpReplies(d *udpDest, w *frameWriter) {
	defer d.dead.Store(true)
	if !vlessin.ReadResponse(d.conn) {
		return
	}
	var hdr [2]byte
	buf := make([]byte, 1500) // grows to the largest packet seen
	for {
		if _, err := io.ReadFull(d.conn, hdr[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[:]))
		if len(buf) < n {
			buf = make([]byte, n)
		}
		if _, err := io.ReadFull(d.conn, buf[:n]); err != nil {
			return
		}
		if err := w.datagram(d.addr, buf[:n]); err != nil {
			return
		}
	}
}

// readFrame reads one mieru-framed datagram into *buf (grown as needed): 0x00, a
// big-endian length, the datagram, 0xff.
func readFrame(br *bufio.Reader, buf *[]byte) (int, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return 0, err
	}
	if hdr[0] != 0x00 {
		return 0, errors.New("mieru: bad datagram prefix")
	}
	n := int(binary.BigEndian.Uint16(hdr[1:]))
	if len(*buf) < n {
		*buf = make([]byte, n)
	}
	if _, err := io.ReadFull(br, (*buf)[:n]); err != nil {
		return 0, err
	}
	if b, err := br.ReadByte(); err != nil || b != 0xff {
		return 0, errors.New("mieru: bad datagram suffix")
	}
	return n, nil
}

// parseDatagram splits a SOCKS5 UDP datagram: RSV RSV FRAG ATYP address port data. raw
// is ATYP through port, as written. Fragments are not supported (FRAG must be 0).
func parseDatagram(b []byte) (raw []byte, host string, port uint16, payload []byte, ok bool) {
	if len(b) < 4 || b[2] != 0 {
		return nil, "", 0, nil, false
	}
	var end int
	switch b[3] {
	case 1:
		end = 4 + 4
		if len(b) < end+2 {
			return nil, "", 0, nil, false
		}
		host = netip.AddrFrom4([4]byte(b[4:8])).String()
	case 4:
		end = 4 + 16
		if len(b) < end+2 {
			return nil, "", 0, nil, false
		}
		host = netip.AddrFrom16([16]byte(b[4:20])).Unmap().String()
	case 3:
		if len(b) < 5 {
			return nil, "", 0, nil, false
		}
		end = 5 + int(b[4])
		if b[4] == 0 || len(b) < end+2 {
			return nil, "", 0, nil, false
		}
		host = string(b[5:end])
	default:
		return nil, "", 0, nil, false
	}
	port = binary.BigEndian.Uint16(b[end:])
	if port == 0 {
		return nil, "", 0, nil, false
	}
	return b[3 : end+2], host, port, b[end+2:], true
}

// frameWriter writes datagrams to the session, one whole frame at a time: the replies
// of every destination share it.
type frameWriter struct {
	mu   sync.Mutex
	conn net.Conn
	buf  []byte
}

// datagram writes payload as a SOCKS5 UDP datagram from addr (ATYP address port).
func (w *frameWriter) datagram(addr, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 3 + len(addr) + len(payload)
	if n > 0xffff {
		return nil // cannot be framed; dropped
	}
	b := append(w.buf[:0], 0x00, byte(n>>8), byte(n), 0, 0, 0)
	b = append(b, addr...)
	b = append(b, payload...)
	b = append(b, 0xff)
	w.buf = b
	_, err := w.conn.Write(b)
	return err
}

// track records a session, if the user has a place for one.
func (s *Server) track(ss *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userSess[ss.user.Email] >= maxSessionsPerUser {
		return false
	}
	s.userSess[ss.user.Email]++
	s.sessions[ss] = struct{}{}
	return true
}

func (s *Server) untrack(ss *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, ss)
	if s.userSess[ss.user.Email] <= 1 {
		delete(s.userSess, ss.user.Email)
	} else {
		s.userSess[ss.user.Email]--
	}
}

// reserve takes a place in a user's count of open connections to Xray, if one is free.
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
