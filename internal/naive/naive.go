// Package naive serves NaiveProxy clients from the decoy behind :443.
//
// A NaiveProxy client is Chrome's network stack talking HTTP/2 to a web server: it opens
// a TLS connection with Chrome's fingerprint and sends CONNECT requests carrying a
// Proxy-Authorization header. Xray already terminates TLS on :443 and hands everything
// that is not VLESS to the panel's decoy over plaintext HTTP/2 (the fallback), so the
// lane needs no port, certificate or process of its own: an authenticated CONNECT that
// reaches the decoy is a tunnel, anything else is the decoy as it always was. A probe
// that sends CONNECT without the right credentials gets the same answer any other odd
// request gets.
//
// The tunnel does not leave the box from here. Each CONNECT stream is handed back to
// Xray as a VLESS connection on a loopback inbound (naive-in), with the user's own UUID
// and a PROXY header naming the client's real address. Xray then does for it what it
// does for every other lane — per-user traffic counters, routing and egress lanes, the
// access log that feeds device limits, speed caps and the connection cut — and the
// panel never needs to know which lane a byte came through. UDP rides the same way when
// the client asks for sing-box's UDP over TCP: each destination becomes a VLESS UDP
// connection (uot.go).
//
// Credentials are the user's existing ones: the login is the Xray email (u<id>) and the
// password the VLESS UUID, so there is nothing new to store and regenerating a user's
// keys rotates this lane with the others.
package naive

import (
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// User is one credential the lane accepts: the login (the Xray email, u<id>) and the
// password, which is the user's VLESS UUID and also what the tunnel authenticates to
// naive-in with.
type User struct {
	Email string
	UUID  string
}

// cred is a user as the lane holds it: with the UUID already in the 16 bytes VLESS sends.
type cred struct {
	User
	id [16]byte
}

// Server answers the NaiveProxy CONNECT requests that reach the decoy. The zero value
// is not usable; build one with New.
type Server struct {
	inner atomic.Pointer[string] // naive-in's loopback address

	users atomic.Pointer[map[string]cred] // password (UUID) → user

	mu          sync.Mutex
	streams     map[*stream]struct{}
	perUser     map[string]int // open connections to Xray, by user (see maxConnsPerUser)
	userStreams map[string]int // open streams, by user (see maxStreamsPerUser)
}

// New returns a server that hands its tunnels to the naive-in inbound at inner: an
// abstract unix socket ("@name") or a loopback "host:port". It accepts nobody until
// SetUsers.
func New(inner string) *Server {
	s := &Server{streams: map[*stream]struct{}{}, perUser: map[string]int{}, userStreams: map[string]int{}}
	s.inner.Store(&inner)
	return s
}

// SetUsers replaces the credentials the lane accepts with those of naive-in's clients,
// and ends the open tunnels of anyone no longer among them. Removing a user from Xray
// only refuses their next connection, so without the second half a disabled user
// would keep every tunnel they already had. addr is where naive-in listens in the same
// config; "" keeps the address the lane has.
func (s *Server) SetUsers(addr string, users []User) {
	if addr != "" {
		s.inner.Store(&addr)
	}
	m := make(map[string]cred, len(users))
	for _, u := range users {
		id, ok := parseUUID(u.UUID)
		if ok && u.Email != "" {
			m[u.UUID] = cred{User: u, id: id}
		}
	}
	s.users.Store(&m)

	s.mu.Lock()
	var gone []*stream
	for st := range s.streams {
		if u, ok := m[st.user.UUID]; !ok || u.Email != st.user.Email {
			gone = append(gone, st)
		}
	}
	s.mu.Unlock()
	for _, st := range gone {
		st.close()
	}
}

// Users is how many credentials the lane accepts now.
func (s *Server) Users() int {
	if m := s.users.Load(); m != nil {
		return len(*m)
	}
	return 0
}

// Wrap puts the lane in front of next, the decoy. Only an HTTP/2 CONNECT with valid
// credentials is a tunnel; every other request — including a CONNECT with wrong or no
// credentials — goes to next untouched.
func (s *Server) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect && r.ProtoMajor == 2 {
			if u, ok := s.auth(r); ok {
				s.serve(w, r, u)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// auth checks the request's Proxy-Authorization against the current credentials.
func (s *Server) auth(r *http.Request) (cred, bool) {
	m := s.users.Load()
	if m == nil || len(*m) == 0 {
		return cred{}, false
	}
	login, pass, ok := basicAuth(r.Header.Get("Proxy-Authorization"))
	if !ok {
		return cred{}, false
	}
	u, ok := (*m)[pass]
	if !ok || u.Email != login {
		return cred{}, false
	}
	return u, true
}

// basicAuth parses a "Basic <base64(login:pass)>" credential.
func basicAuth(h string) (login, pass string, ok bool) {
	const prefix = "basic "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[len(prefix):]))
	if err != nil {
		return "", "", false
	}
	login, pass, ok = strings.Cut(string(raw), ":")
	return login, pass, ok
}

func (s *Server) track(st *stream) {
	s.mu.Lock()
	s.streams[st] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) untrack(st *stream) {
	s.mu.Lock()
	delete(s.streams, st)
	s.mu.Unlock()
}

// Open is how many tunnels are open now.
func (s *Server) Open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

// innerNetwork is the network naive-in's address is dialed on.
func innerNetwork(addr string) string {
	if strings.HasPrefix(addr, "@") {
		return "unix"
	}
	return "tcp"
}

// HTTP2Config is what the HTTP/2 server in front of the lane should run with. Go hands a
// connection's receive window back only as a handler reads, and a client's tunnels share
// one connection: with the connection's window no larger than one stream's (Go's
// default), a single tunnel whose target stopped reading holds all of it and stalls
// every other upload of that client. Room for several stalled streams keeps them moving;
// a stream's own window, and so the memory one tunnel can pin, stays Go's.
func HTTP2Config() *http.HTTP2Config {
	return &http.HTTP2Config{MaxReceiveBufferPerConnection: 4 << 20}
}
