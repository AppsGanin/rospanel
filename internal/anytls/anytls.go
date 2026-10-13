// Package anytls answers AnyTLS (github.com/anytls) behind the :443 decoy and hands every
// tunnel to Xray's internal anytls-in VLESS inbound (see vlessin), so Xray does the
// stats, routing, device limits and speed caps as for any lane.
//
// An AnyTLS client opens TLS to the server — Xray's TCP-TLS lane, whose fallback hands
// whatever is not VLESS to the panel's listener — and starts with the SHA-256 of its
// password. WrapListener sits on that listener: a connection that authenticates is a
// session here, anything else (the decoy's visitors, NaiveProxy) leaves through the
// returned listener with the bytes it sent, for the HTTP server behind.
//
// A user's password is their UUID. The password is checked once per connection, so a
// user removed — or given a new UUID — has the connections they hold closed here; the
// library names a session's user by email and the UUID it authenticated with, so a
// session is never taken for one on a newer UUID.
package anytls

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sanytls "github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing/common/auth"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/AppsGanin/rospanel/internal/vlessin"
)

// User is one account the lane lets in: Email ("u<id>") names them in Xray, UUID is the
// password and the VLESS id the tunnel enters Xray with.
type User struct {
	Email string
	UUID  string
}

// cred is a user as the server checks them.
type cred struct {
	User
	id [16]byte
}

// firstByteTimeout bounds how long a new connection may take to send its first bytes,
// AnyTLS or HTTP: the read happens before the HTTP server, and its own header timeout,
// see the connection.
const firstByteTimeout = 15 * time.Second

// Server is the AnyTLS lane. The zero value is not usable; see New.
type Server struct {
	inner atomic.Pointer[string]          // anytls-in's address
	svc   *sanytls.MultiService[string]   // users keyed by userKey
	users atomic.Pointer[map[string]cred] // by email

	mu       sync.Mutex
	sessions map[*session]struct{}
	perUser  map[string]int // open connections to Xray, by user
	fallback chan net.Conn  // what is not AnyTLS, for WrapListener's listener
}

// New returns a server that hands its tunnels to the anytls-in inbound at inner: an
// abstract unix socket ("@name") or a loopback "host:port". It accepts nobody until
// SetUsers.
func New(inner string) (*Server, error) {
	s := &Server{sessions: map[*session]struct{}{}, perUser: map[string]int{}, fallback: make(chan net.Conn)}
	s.inner.Store(&inner)
	empty := map[string]cred{}
	s.users.Store(&empty)
	svc, err := sanytls.NewMultiService[string](sanytls.ServiceOptions{
		Handler:         handler{s},
		FallbackHandler: fallbackHandler{s},
	})
	if err != nil {
		return nil, err
	}
	s.svc = svc
	return s, nil
}

// SetUsers replaces the accounts the lane lets in with those of anytls-in's clients, and
// closes the connections of anyone no longer among them or whose UUID changed. addr is
// where anytls-in listens in the same config; "" keeps the address the lane has.
func (s *Server) SetUsers(addr string, users []User) {
	m := make(map[string]cred, len(users))
	keys := make([]string, 0, len(users))
	passwords := make([]string, 0, len(users))
	for _, u := range users {
		id, ok := vlessin.ParseUUID(u.UUID)
		if !ok || u.Email == "" {
			continue
		}
		m[u.Email] = cred{User: u, id: id}
		keys = append(keys, userKey(u))
		passwords = append(passwords, u.UUID)
	}
	s.mu.Lock()
	if addr != "" {
		s.inner.Store(&addr)
	}
	s.users.Store(&m)
	_ = s.svc.UpdateUsers(keys, passwords)
	var gone []*session
	for ss := range s.sessions {
		if c, ok := m[ss.email]; !ok || c.UUID != ss.uuid {
			gone = append(gone, ss)
		}
	}
	s.mu.Unlock()
	for _, ss := range gone {
		go ss.close()
	}
}

// Users is how many accounts the lane lets in.
func (s *Server) Users() int { return len(*s.users.Load()) }

// Open is how many authenticated connections are open.
func (s *Server) Open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// WrapListener returns a listener of what is not AnyTLS on ln. Each connection ln
// accepts is offered to the lane; one that does not authenticate comes out of the
// returned listener carrying the bytes it already sent. With no users the lane reads
// nothing and every connection passes straight through. One wrap per server.
func (s *Server) WrapListener(ln net.Listener) net.Listener {
	w := &wrapped{Listener: ln, s: s, done: make(chan struct{})}
	go w.acceptLoop()
	return w
}

type wrapped struct {
	net.Listener
	s    *Server
	done chan struct{}
	once sync.Once
	err  atomic.Pointer[error]
}

// acceptLoop ends only when the listener is closed. Anything else — out of file
// descriptors under a flood — is waited out as net/http does, since every HTTP answer
// the panel gives comes through here.
func (w *wrapped) acceptLoop() {
	var delay time.Duration
	for {
		c, err := w.Listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				w.err.Store(&err)
				w.once.Do(func() { close(w.done) })
				return
			}
			delay = min(max(2*delay, 5*time.Millisecond), time.Second)
			select {
			case <-time.After(delay):
			case <-w.done:
				return
			}
			continue
		}
		delay = 0
		go w.s.serveConn(c)
	}
}

// Accept returns the next connection that is not AnyTLS.
func (w *wrapped) Accept() (net.Conn, error) {
	select {
	case c := <-w.s.fallback:
		return c, nil
	case <-w.done:
		if p := w.err.Load(); p != nil {
			return nil, *p
		}
		return nil, net.ErrClosed
	}
}

func (w *wrapped) Close() error {
	err := w.Listener.Close()
	w.once.Do(func() { close(w.done) })
	return err
}

// serveConn offers one connection to the lane.
func (s *Server) serveConn(c net.Conn) {
	if s.Users() == 0 {
		s.toFallback(c)
		return
	}
	ss := &session{conn: c}
	// The address first: behind a PROXY-protocol listener reading it reads the header,
	// which clears a deadline set before it.
	source := M.SocksaddrFromNet(c.RemoteAddr())
	_ = c.SetReadDeadline(time.Now().Add(firstByteTimeout))
	fc := &firstReadConn{Conn: c}
	ctx := context.WithValue(context.Background(), sessionKey{}, ss)
	_ = s.svc.NewConnection(ctx, fc, source, func(error) {
		s.untrack(ss)
		_ = c.Close()
	})
}

// toFallback hands a connection to WrapListener's listener, or closes it when nothing
// accepts any more.
func (s *Server) toFallback(c net.Conn) {
	select {
	case s.fallback <- c:
	case <-time.After(time.Minute):
		_ = c.Close()
	}
}

// firstReadConn lifts the first-bytes deadline once they have come: an AnyTLS session
// lives on, and the HTTP server sets its own.
type firstReadConn struct {
	net.Conn
	once sync.Once
}

func (c *firstReadConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.once.Do(func() { _ = c.SetReadDeadline(time.Time{}) })
	}
	return n, err
}

type sessionKey struct{}

// session is one authenticated connection, tracked so SetUsers can close it.
type session struct {
	conn  net.Conn
	email string
	uuid  string

	mu     sync.Mutex
	closed bool
}

func (ss *session) close() {
	ss.mu.Lock()
	if ss.closed {
		ss.mu.Unlock()
		return
	}
	ss.closed = true
	ss.mu.Unlock()
	_ = ss.conn.Close()
}

// userKey names a user to the library: email and the UUID that is their password.
func userKey(u User) string { return u.Email + "\x00" + u.UUID }

// track records a session's user — as it authenticated, key being userKey — the first
// time one of its streams arrives, and reports whether that user is still let in on
// that UUID.
func (s *Server) track(ss *session, key string) (cred, bool) {
	email, uuid, _ := strings.Cut(key, "\x00")
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := (*s.users.Load())[email]
	if !ok || c.UUID != uuid {
		return cred{}, false
	}
	if _, tracked := s.sessions[ss]; !tracked {
		ss.email, ss.uuid = email, uuid
		s.sessions[ss] = struct{}{}
	}
	return c, true
}

func (s *Server) untrack(ss *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, ss)
}

// fallbackHandler takes what did not authenticate.
type fallbackHandler struct{ s *Server }

func (h fallbackHandler) NewConnectionEx(_ context.Context, conn net.Conn, _ M.Socksaddr, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	h.s.toFallback(conn)
}

// handler takes each stream of an authenticated session.
type handler struct{ s *Server }

func (h handler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	err := h.s.serveStream(ctx, conn, source, destination)
	_ = conn.Close()
	if onClose != nil {
		onClose(err)
	}
}

var errUnknownUser = errors.New("anytls: user not let in")

// serveStream carries one stream: TCP, or sing-box's UDP over TCP. The stream is
// answered (SYNACK) once its way into Xray is open: a client speaking protocol v2 waits
// three seconds for that, or for any frame, and then drops its whole session — which a
// destination slow to send its first byte would otherwise cause.
func (s *Server) serveStream(ctx context.Context, conn net.Conn, source, destination M.Socksaddr) error {
	key, _ := auth.UserFromContext[string](ctx)
	ss, _ := ctx.Value(sessionKey{}).(*session)
	if ss == nil {
		return N.ReportHandshakeFailure(conn, errUnknownUser)
	}
	u, ok := s.track(ss, key)
	if !ok {
		go ss.close()
		return errUnknownUser
	}
	if isUoT(destination) {
		_ = N.ReportConnHandshakeSuccess(conn, conn)
		return s.serveUoT(conn, u, source, destination)
	}
	host, port := target(destination)
	if host == "" {
		return N.ReportHandshakeFailure(conn, errors.New("anytls: bad destination"))
	}
	xc, err := s.dialXray(u, source, vlessin.CmdTCP, host, port)
	if err != nil {
		return N.ReportHandshakeFailure(conn, err)
	}
	defer xc.Close()
	_ = N.ReportConnHandshakeSuccess(conn, conn)
	return pipe(conn, xc)
}
