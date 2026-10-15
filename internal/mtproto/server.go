// Package mtproto is a Telegram proxy (MTProto with fake TLS, the "ee" secrets) on a
// port of its own. Every user has a secret of their own, derived from their UUID, so a
// user the panel stops serving is stopped here too; the connection to Telegram goes out
// through Xray's internal mtproto-in VLESS inbound (see vlessin), so Xray does the
// stats, routing, device limits and speed caps as for any lane.
//
// A client is told apart by its ClientHello alone: its random is an HMAC under one of
// the users' secrets. Anything else — a browser, a scanner, a replayed hello — is handed
// to the site the secret names (the server's own decoy when that is its domain) with
// the bytes it sent, so it sees that site's real certificate and pages.
package mtproto

import (
	"bufio"
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AppsGanin/rospanel/internal/vlessin"
)

const (
	// handshakeTimeout bounds the fake TLS handshake and the obfuscated frame after it.
	handshakeTimeout = 10 * time.Second
	dialTimeout      = 5 * time.Second
	// skew is how far a hello's time may be from ours. Hellos seen within twice that are
	// remembered, so a replay inside the window is caught too.
	skew = time.Minute
	// maxConnsPerUser bounds a user's open connections. A Telegram client keeps several
	// per data centre, more while it loads media.
	maxConnsPerUser = 1024
	// maxHandshakes bounds the handshakes in progress at once — only those: a slot is
	// given back once the connection is a tunnel or has been handed to the front site.
	maxHandshakes = 1024
	// maxFronted bounds the connections handed to the front site at once.
	maxFronted = 1024
	// firstByteTimeout is how long a connection may stay silent before it says anything
	// — Xray's own handshake wait, as the TLS front has (internal/tlsfront): a client
	// that connects and says nothing is closed after as long as anywhere else on the box.
	firstByteTimeout = 60 * time.Second
	// frontIdle closes a connection handed to the front site after this long with
	// nothing moving either way.
	frontIdle = 2 * time.Minute
	// writeTimeout bounds one write to a client: one that stops reading frees its
	// tunnel rather than holding it for good.
	writeTimeout = 5 * time.Minute
	// maxHelloScan is the largest hello checked against the users' secrets. Telegram's
	// is about 517 bytes; a larger one is handed to the front site without paying an
	// HMAC per user for it.
	maxHelloScan = 4096
)

// User is one account the lane lets in: Email ("u<id>") names them to Xray, UUID is
// their VLESS id and what their secret derives from.
type User struct {
	Email string
	UUID  string
}

// Listen is where and how the lane answers. A zero Port is off. Domain is the name in
// the users' secrets — what their clients put in the SNI; Front is the host:port a
// connection that is not a client of the lane is handed to.
type Listen struct {
	Port   int
	Domain string
	Front  string
}

// Secret is a user's 16-byte proxy secret: derived from the UUID one way, so a link
// passed around reveals nothing of the account, and a UUID reset gives a new one.
func Secret(uuid string) [16]byte {
	sum := sha256.Sum256([]byte("rospanel-mtproto:" + uuid))
	var s [16]byte
	copy(s[:], sum[:16])
	return s
}

// SecretHex is the secret as a Telegram client takes it: "ee", the key, the domain.
func SecretHex(uuid, domain string) string {
	s := Secret(uuid)
	return "ee" + hex.EncodeToString(s[:]) + hex.EncodeToString([]byte(domain))
}

// Link is the tg://proxy link that adds the proxy to Telegram in one tap.
func Link(host string, port int, domain, uuid string) string {
	q := url.Values{}
	q.Set("server", host)
	q.Set("port", strconv.Itoa(port))
	q.Set("secret", SecretHex(uuid, domain))
	return "tg://proxy?" + q.Encode()
}

// WebLink is Link as an https://t.me address, for a page opened outside Telegram.
func WebLink(host string, port int, domain, uuid string) string {
	return "https://t.me/proxy?" + Link(host, port, domain, uuid)[len("tg://proxy?"):]
}

type cred struct {
	User
	id     [16]byte
	secret [16]byte
}

type table struct {
	list    []*cred
	byEmail map[string]*cred
}

// Server is the MTProto lane. The zero value is not usable; see New.
type Server struct {
	inner   atomic.Pointer[string]
	users   atomic.Pointer[table]
	slots   chan struct{} // handshakes in progress
	fronted chan struct{} // connections with the front site

	mu       sync.Mutex
	ln       net.Listener
	listen   Listen // where it listens now
	want     Listen // where Apply last asked it to
	closed   bool
	sessions map[*session]struct{}
	perUser  map[string]int
	fronts   map[net.Conn]struct{} // connections handed to the front site, for Close

	replay replayCache
	seen   ipCache
}

// New returns a server that hands its tunnels to the mtproto-in inbound at inner: an
// abstract unix socket ("@name") or a loopback "host:port". It listens nowhere until
// Apply.
func New(inner string) *Server {
	s := &Server{sessions: map[*session]struct{}{}, perUser: map[string]int{}, fronts: map[net.Conn]struct{}{},
		slots: make(chan struct{}, maxHandshakes), fronted: make(chan struct{}, maxFronted)}
	s.inner.Store(&inner)
	s.users.Store(&table{byEmail: map[string]*cred{}})
	return s
}

// Apply makes the lane answer on l for users and ends the open connections of anyone no
// longer among them, or whose UUID changed. A change of port restarts the listener;
// open connections are not its, and stay. addr is where mtproto-in listens in the same
// config; "" keeps the address the lane has.
func (s *Server) Apply(l Listen, addr string, users []User) error {
	t := &table{byEmail: make(map[string]*cred, len(users))}
	for _, u := range users {
		id, ok := vlessin.ParseUUID(u.UUID)
		if !ok || u.Email == "" {
			continue
		}
		c := &cred{User: u, id: id, secret: Secret(u.UUID)}
		t.list = append(t.list, c)
		t.byEmail[u.Email] = c
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if addr != "" {
		s.inner.Store(&addr)
	}
	s.users.Store(t)
	s.want = l
	var old net.Listener
	var err error
	if l.Port != s.listen.Port || (s.ln == nil && l.Port > 0) {
		old, s.ln, s.listen = s.ln, nil, Listen{}
		if l.Port > 0 {
			err = s.startLocked(l)
		}
	} else if s.ln != nil {
		s.listen = l // the domain or the front changed: the listener stays
	}
	var gone []*session
	for ss := range s.sessions {
		if c, ok := t.byEmail[ss.user.Email]; !ok || c.UUID != ss.user.UUID {
			gone = append(gone, ss)
		}
	}
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	for _, ss := range gone {
		ss.close()
	}
	return err
}

// startLocked listens on l. Caller holds mu.
func (s *Server) startLocked(l Listen) error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(l.Port))
	if err != nil {
		return fmt.Errorf("mtproto: listen on %d: %w", l.Port, err)
	}
	s.ln, s.listen = ln, l
	go s.acceptLoop(ln)
	return nil
}

// Retry starts the listener Apply last asked for, if it is not up (its port was taken,
// say).
func (s *Server) Retry() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ln != nil || s.want.Port <= 0 {
		return nil
	}
	return s.startLocked(s.want)
}

// Close stops the lane for good and ends every connection.
func (s *Server) Close() {
	s.mu.Lock()
	ln := s.ln
	s.ln, s.listen, s.closed = nil, Listen{}, true
	all := make([]*session, 0, len(s.sessions))
	for ss := range s.sessions {
		all = append(all, ss)
	}
	fronts := make([]net.Conn, 0, len(s.fronts))
	for c := range s.fronts {
		fronts = append(fronts, c)
	}
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	for _, ss := range all {
		ss.close()
	}
	for _, c := range fronts {
		_ = c.Close()
	}
}

// Listening is where the lane answers now; a zero Listen when it does not.
func (s *Server) Listening() Listen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listen
}

// Users is how many accounts the lane lets in.
func (s *Server) Users() int { return len(s.users.Load().list) }

// Open is how many connections to Telegram are open.
func (s *Server) Open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *Server) acceptLoop(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			current := s.ln == ln
			s.mu.Unlock()
			if !current {
				return
			}
			// Out of file descriptors, say: the listener is still ours, so keep at it
			// rather than leave a bound port nothing accepts on.
			slog.Warn("mtproto: accept failed", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		select {
		case s.slots <- struct{}{}:
			go s.handle(c)
		default:
			_ = c.Close()
		}
	}
}

// handle runs one connection: the handshake, then the tunnel — or the front site. It
// holds a handshake slot until it knows which, and gives it back then.
func (s *Server) handle(c net.Conn) {
	var once sync.Once
	release := func() { once.Do(func() { <-s.slots }) }
	defer release()
	l := s.Listening()
	var sent bytes.Buffer // what the client sent, for the front site if it is not ours
	// The first byte under the long wait, the rest of the hello under the short one: a
	// silent connection is closed as late as anything else here would close it, and is
	// not handed on to dial the front site for nothing.
	_ = c.SetReadDeadline(time.Now().Add(firstByteTimeout))
	var first [1]byte
	if _, err := io.ReadFull(c, first[:]); err != nil {
		_ = c.Close()
		return
	}
	sent.WriteByte(first[0])
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	hello, err := readClientHello(io.MultiReader(bytes.NewReader(first[:]), io.TeeReader(c, &sent)))
	var u *cred
	if err == nil && len(hello.raw) <= maxHelloScan && hello.hasSNI(l.Domain) {
		u = s.match(c.RemoteAddr(), hello)
	}
	if u == nil || s.replay.seen(hello.random) {
		release()
		s.front(c, l.Front, sent.Bytes())
		return
	}
	if _, err := c.Write(serverHello(u.secret[:], hello)); err != nil {
		_ = c.Close()
		return
	}
	rc := newRecordConn(bufio.NewReaderSize(c, maxRecord+5), c)
	dc, recv, send, err := readClientFrame(rc, u.secret[:])
	if err != nil {
		_ = c.Close()
		return
	}
	_ = c.SetDeadline(time.Time{})
	release()
	s.serve(c, rc, u, dc, recv, send)
}

// match finds whose secret the hello was made with: the user last seen from the same
// address first, then everyone.
func (s *Server) match(addr net.Addr, h *clientHello) *cred {
	t := s.users.Load()
	now := time.Now()
	ip := hostOf(addr)
	var tried *cred
	if email, ok := s.seen.get(ip); ok {
		if c := t.byEmail[email]; c != nil {
			if h.verify(c.secret[:], now, skew) {
				return c
			}
			tried = c
		}
	}
	for _, c := range t.list {
		if c != tried && h.verify(c.secret[:], now, skew) {
			s.seen.put(ip, c.Email)
			return c
		}
	}
	return nil
}

func hostOf(a net.Addr) string {
	if ta, ok := a.(*net.TCPAddr); ok {
		return ta.IP.String()
	}
	h, _, _ := net.SplitHostPort(a.String())
	return h
}

// front hands a connection that is not the lane's to the front site, with what it sent.
func (s *Server) front(c net.Conn, addr string, sent []byte) {
	defer c.Close()
	if addr == "" {
		return
	}
	select {
	case s.fronted <- struct{}{}:
		defer func() { <-s.fronted }()
	default:
		return
	}
	if !s.trackFront(c) {
		return
	}
	defer s.untrackFront(c)
	f, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Write(sent); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	act := &activity{}
	act.touch()
	pipe(&idleConn{Conn: c, act: act}, &idleConn{Conn: f, act: act})
}

func (s *Server) trackFront(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.fronts[c] = struct{}{}
	return true
}

func (s *Server) untrackFront(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fronts, c)
}

// pipe copies both ways as a TCP proxy would: a side that is done sending has the other
// side's sending half closed, so a request followed by a half-close still gets its
// answer; both are closed once both directions are done, or either fails.
func pipe(a, b *idleConn) {
	done := make(chan error, 2)
	run := func(dst, src *idleConn) {
		_, err := io.Copy(dst, src)
		if err == nil {
			if cw, ok := dst.Conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			}
		}
		done <- err
	}
	go run(a, b)
	go run(b, a)
	if err := <-done; err != nil {
		_ = a.Close()
		_ = b.Close()
	}
	<-done
	_ = a.Close()
	_ = b.Close()
}

// activity is when anything last moved on a fronted connection, either way.
type activity struct{ at atomic.Int64 }

func (a *activity) touch()               { a.at.Store(time.Now().UnixNano()) }
func (a *activity) quiet() time.Duration { return time.Since(time.Unix(0, a.at.Load())) }

// idleConn is one side of a fronted connection: closed once nothing has moved either
// way for frontIdle — a long download one way keeps the other side open.
type idleConn struct {
	net.Conn
	act *activity
}

func (c *idleConn) Read(p []byte) (int, error) {
	for {
		_ = c.SetReadDeadline(time.Now().Add(frontIdle))
		n, err := c.Conn.Read(p)
		if n > 0 {
			c.act.touch()
		}
		var ne net.Error
		if err != nil && errors.As(err, &ne) && ne.Timeout() && n == 0 && c.act.quiet() < frontIdle {
			continue // the other way is busy
		}
		return n, err
	}
}

func (c *idleConn) Write(p []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.act.touch()
	}
	return n, err
}

// session is one client connection carried to Telegram, tracked so Apply can end it.
type session struct {
	user   User
	client net.Conn

	mu     sync.Mutex
	xray   net.Conn
	closed bool
}

func (ss *session) close() {
	ss.mu.Lock()
	if ss.closed {
		ss.mu.Unlock()
		return
	}
	ss.closed = true
	x := ss.xray
	ss.mu.Unlock()
	_ = ss.client.Close()
	if x != nil {
		_ = x.Close()
	}
}

func (ss *session) attach(x net.Conn) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return false
	}
	ss.xray = x
	return true
}

// track registers a session, or refuses it when its user has too many open or is no
// longer let in.
func (s *Server) track(ss *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.users.Load().byEmail[ss.user.Email]
	if !ok || c.UUID != ss.user.UUID || s.perUser[ss.user.Email] >= maxConnsPerUser || s.closed {
		return false
	}
	s.sessions[ss] = struct{}{}
	s.perUser[ss.user.Email]++
	return true
}

func (s *Server) untrack(ss *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[ss]; !ok {
		return
	}
	delete(s.sessions, ss)
	if s.perUser[ss.user.Email]--; s.perUser[ss.user.Email] <= 0 {
		delete(s.perUser, ss.user.Email)
	}
}

// serve carries the client's stream to the data centre it asked for, through Xray.
func (s *Server) serve(c net.Conn, rc *recordConn, u *cred, dc int, recv, send cipher.Stream) {
	ss := &session{user: u.User, client: c}
	if !s.track(ss) {
		_ = c.Close()
		return
	}
	defer s.untrack(ss)
	defer ss.close()

	addrs, dc := dcAddresses(dc)
	host, port, _ := net.SplitHostPort(addrs[0])
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	inner := *s.inner.Load()
	x, err := (&net.Dialer{}).DialContext(ctx, vlessin.Network(inner), inner)
	cancel()
	if err != nil {
		return
	}
	if !ss.attach(x) {
		_ = x.Close()
		return
	}
	opening, dcSend, dcRecv := openFrame(dc, nil)
	req := vlessin.ProxyHeader(c.RemoteAddr().String())
	req = vlessin.AppendRequest(req, u.id, vlessin.CmdTCP, host, uint16(p))
	if _, err := x.Write(append(req, opening...)); err != nil {
		return
	}

	up := make(chan struct{})
	go func() {
		defer close(up)
		buf := make([]byte, 32*1024)
		for {
			n, err := rc.Read(buf)
			if n > 0 {
				recv.XORKeyStream(buf[:n], buf[:n])
				dcSend.XORKeyStream(buf[:n], buf[:n])
				if _, werr := x.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if cw, ok := x.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	if vlessin.ReadResponse(x) {
		buf := make([]byte, maxRecordPayload)
		for {
			n, err := x.Read(buf)
			if n > 0 {
				dcRecv.XORKeyStream(buf[:n], buf[:n])
				send.XORKeyStream(buf[:n], buf[:n])
				_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
				if _, werr := rc.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}
	ss.close() // Telegram is done: so is the client, and with it the upload
	<-up
}

// replayCache remembers the randoms of the hellos let in for at least twice the skew —
// as long as one can pass the time check after it was first seen (a client clock ahead
// of ours by up to the skew, behind by up to the skew): a hello seen again is a replay,
// by a prober testing what answers here.
type replayCache struct {
	mu        sync.Mutex
	cur, prev map[[randomLen]byte]struct{}
	rotated   time.Time
}

func (r *replayCache) seen(random [randomLen]byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil || time.Since(r.rotated) > 2*skew {
		r.prev, r.cur, r.rotated = r.cur, map[[randomLen]byte]struct{}{}, time.Now()
	}
	if _, ok := r.cur[random]; ok {
		return true
	}
	if _, ok := r.prev[random]; ok {
		return true
	}
	r.cur[random] = struct{}{}
	return false
}

// ipCache remembers which user an address last connected as, so their hello is tried
// against that secret first rather than everyone's.
type ipCache struct {
	mu sync.Mutex
	m  map[string]string
}

const ipCacheMax = 1 << 16

func (c *ipCache) get(ip string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[ip]
	return e, ok
}

func (c *ipCache) put(ip, email string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= ipCacheMax {
		c.m = map[string]string{}
	}
	c.m[ip] = email
}
