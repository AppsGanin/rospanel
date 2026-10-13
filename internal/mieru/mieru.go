// Package mieru answers the mieru protocol (github.com/enfein/mieru) on a port of its
// own and hands every tunnel to Xray's internal mieru-in VLESS inbound (see vlessin), so
// Xray does the stats, routing, device limits and speed caps as for any lane.
//
// A user's mieru name is their email ("u<id>") and their password is their UUID: no
// credential of its own. The server is mieru's own multiplexer — the one its server,
// mita, runs — rather than its server API: the API cannot change users once started,
// and the panel changes them all the time; the multiplexer swaps them atomically.
// Removing a user stops new sessions there; their open ones are ended here.
package mieru

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"

	apicommon "github.com/enfein/mieru/v3/apis/common"
	"github.com/enfein/mieru/v3/apis/trafficpattern"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlcommon"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	mierulog "github.com/enfein/mieru/v3/pkg/log"
	"github.com/enfein/mieru/v3/pkg/protocol"
	"google.golang.org/protobuf/proto"

	"github.com/AppsGanin/rospanel/internal/vlessin"
)

// Transports a port can be served on.
const (
	TCP = "TCP"
	UDP = "UDP"
)

// mtu bounds a UDP underlay's packets; mieru's own default.
const mtu = 1400

func init() {
	// mieru logs through a logger of its own; the panel has nothing to read there, and
	// at FATAL its Info lines are not even formatted.
	mierulog.SetFormatter(&mierulog.NilFormatter{})
	mierulog.SetLevel("FATAL")
}

// User is one account the lane lets in: Email ("u<id>") is the mieru user name, UUID the
// password and the VLESS id the tunnel enters Xray with.
type User struct {
	Email string
	UUID  string
}

// Listen is where the lane answers: a port and the transport on it. A zero Port is off.
type Listen struct {
	Port      int
	Transport string
}

// cred is a user as the server checks them.
type cred struct {
	User
	id [16]byte
}

// Server is the mieru lane. The zero value is not usable; see New.
type Server struct {
	inner atomic.Pointer[string] // mieru-in's address
	users atomic.Pointer[map[string]cred]

	mu       sync.Mutex
	mux      *protocol.Mux
	listen   Listen // where it listens now
	want     Listen // where Apply last asked it to
	closed   bool   // Close was called: the lane stays down
	sessions map[*session]struct{}
	perUser  map[string]int // open connections to Xray, by user (see maxConnsPerUser)
	userSess map[string]int // open sessions, by user (see maxSessionsPerUser)
	// known is the last password each name had, kept after the name is gone: a client
	// that connected with it may still hold that connection open (see Apply).
	known map[string]string
	raw   *rawConns // the listener's TCP connections, by the users they carried
}

// New returns a server that hands its tunnels to the mieru-in inbound at inner: an
// abstract unix socket ("@name") or a loopback "host:port". It listens nowhere until
// Apply.
func New(inner string) *Server {
	s := &Server{sessions: map[*session]struct{}{}, perUser: map[string]int{}, userSess: map[string]int{}, known: map[string]string{},
		raw: newRawConns()}
	s.inner.Store(&inner)
	return s
}

// Apply makes the lane answer on l for users, and ends the open sessions of anyone no
// longer among them. A change of port or transport restarts the listener (and with it
// every session); a change of users does not. A name with a password it did not have
// before — a UUID reset, or one coming back after a while gone — has the connections
// that carried its sessions closed (rawConns): mieru checks a client's password once
// per connection, so one still connected with the old password would keep opening
// sessions on it under that name. addr is where mieru-in listens in the same config;
// "" keeps the address the lane has.
func (s *Server) Apply(l Listen, addr string, users []User) error {
	m := make(map[string]cred, len(users))
	for _, u := range users {
		id, ok := vlessin.ParseUUID(u.UUID)
		if ok && u.Email != "" {
			m[u.Email] = cred{User: u, id: id}
		}
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if addr != "" {
		s.inner.Store(&addr)
	}
	var rekeyed []string
	for name, c := range m {
		if was, ok := s.known[name]; ok && was != c.UUID {
			rekeyed = append(rekeyed, name)
		}
		s.known[name] = c.UUID
	}
	s.users.Store(&m)
	s.want = l
	var old *protocol.Mux
	var err error
	if l != s.listen || (s.mux == nil && l.Port > 0) {
		old, s.mux, s.listen = s.mux, nil, Listen{}
		if l.Port > 0 {
			var mux *protocol.Mux
			if mux, err = start(l, m, s.raw); err == nil {
				s.mux, s.listen = mux, l
				go s.acceptLoop(mux)
			}
		}
	} else if s.mux != nil {
		s.mux.SetServerUsers(mieruUsers(m))
	}
	var gone []*session
	for ss := range s.sessions {
		if c, ok := m[ss.user.Email]; !ok || c.UUID != ss.user.UUID {
			gone = append(gone, ss)
		}
	}
	s.mu.Unlock()
	if old != nil {
		_ = old.Close() // its sessions go with it
	}
	// After the new passwords are in: a connection made from here on proves the new one.
	for _, name := range rekeyed {
		s.raw.rekey(name)
	}
	s.raw.forget()
	// Concurrently: mieru closes a session gracefully, waiting up to a second for what it
	// has queued, and this runs on the way to applying the panel's config.
	for _, ss := range gone {
		go ss.close()
	}
	return err
}

// Retry starts the listener Apply last asked for, if it is not up (its port was taken,
// say) — what Apply would do, without waiting for the next change to call it.
func (s *Server) Retry() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.mux != nil || s.want.Port <= 0 {
		return nil
	}
	m := s.users.Load()
	if m == nil {
		return nil
	}
	mux, err := start(s.want, *m, s.raw)
	if err != nil {
		return err
	}
	s.mux, s.listen = mux, s.want
	go s.acceptLoop(mux)
	return nil
}

// Close stops the lane for good and ends every session.
func (s *Server) Close() {
	s.mu.Lock()
	mux := s.mux
	s.mux, s.listen, s.closed = nil, Listen{}, true
	s.mu.Unlock()
	if mux != nil {
		_ = mux.Close()
	}
}

// Listening is where the lane answers now; a zero Listen when it does not.
func (s *Server) Listening() Listen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listen
}

// Users is how many accounts the lane lets in.
func (s *Server) Users() int {
	if m := s.users.Load(); m != nil {
		return len(*m)
	}
	return 0
}

// Open is how many sessions are open.
func (s *Server) Open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// start opens a multiplexer on l.
func start(l Listen, users map[string]cred, raw *rawConns) (*protocol.Mux, error) {
	transport := appctlpb.TransportProtocol_TCP
	switch l.Transport {
	case TCP, "":
	case UDP:
		transport = appctlpb.TransportProtocol_UDP
	default:
		return nil, fmt.Errorf("mieru: unknown transport %q", l.Transport)
	}
	endpoints, err := appctlcommon.AddrPortToUnderlayProperties("",
		[]*appctlpb.PortBinding{{Port: proto.Int32(int32(l.Port)), Protocol: transport.Enum()}}, mtu)
	if err != nil {
		return nil, err
	}
	pattern, err := trafficpattern.NewConfig(nil)
	if err != nil {
		return nil, err
	}
	mux := protocol.NewMux(false).
		SetTrafficPattern(pattern).
		SetServerUsers(mieruUsers(users)).
		SetStreamListenerFactory(newListenerFactory(raw))
	mux.SetEndpoints(endpoints)
	if err := mux.Start(); err != nil {
		return nil, fmt.Errorf("mieru: listen on %s %d: %w", l.Transport, l.Port, err)
	}
	return mux, nil
}

// mieruUsers is the user table the multiplexer authenticates against. It refuses to run
// with none, so an empty lane holds one nobody knows the password of.
func mieruUsers(m map[string]cred) map[string]*appctlpb.User {
	out := make(map[string]*appctlpb.User, max(len(m), 1))
	for name, c := range m {
		out[name] = &appctlpb.User{Name: proto.String(name), Password: proto.String(c.UUID)}
	}
	if len(out) == 0 {
		name, pass := randomHex(8), randomHex(32)
		out[name] = &appctlpb.User{Name: proto.String(name), Password: proto.String(pass)}
	}
	return out
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// acceptLoop takes the sessions mux opens until it is closed.
func (s *Server) acceptLoop(mux *protocol.Mux) {
	for {
		c, err := mux.Accept()
		if err != nil {
			s.mu.Lock()
			current := s.mux == mux
			s.mu.Unlock()
			if !current {
				return
			}
			slog.Warn("mieru: accept failed", "err", err)
			continue
		}
		go s.handle(c)
	}
}

// lookup is the current credentials of the session's user, if they are still let in.
func (s *Server) lookup(c net.Conn) (cred, bool) {
	uc, ok := c.(apicommon.UserContext)
	if !ok {
		return cred{}, false
	}
	m := s.users.Load()
	if m == nil {
		return cred{}, false
	}
	u, ok := (*m)[uc.UserName()]
	return u, ok
}

// accepts reports whether u is still among the current credentials.
func (s *Server) accepts(u User) bool {
	m := s.users.Load()
	if m == nil {
		return false
	}
	c, ok := (*m)[u.Email]
	return ok && c.UUID == u.UUID
}
