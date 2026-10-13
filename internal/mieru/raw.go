package mieru

import (
	"context"
	"net"
	"sync"
	"time"

	apicommon "github.com/enfein/mieru/v3/apis/common"
	"github.com/enfein/mieru/v3/pkg/sockopts"
)

// A mieru client proves who it is once per TCP connection — the underlay its sessions
// share — and keeps opening sessions on it under that name. A user given a new password
// (a UUID reset) would so keep the old one working for as long as that connection
// lasts. The connections are tracked here, each with the users whose sessions it
// carried, so that a reset closes that user's alone: the multiplexer and everyone
// else's connections stay up.

// rawConns is every TCP connection the listener has open, by the client's address —
// what a session's RemoteAddr names.
type rawConns struct {
	mu sync.Mutex
	m  map[string]*rawConn
	// rekeyed is when each user was last given a new password: a connection older
	// than that may hold the old one.
	rekeyed map[string]time.Time
}

func newRawConns() *rawConns {
	return &rawConns{m: map[string]*rawConn{}, rekeyed: map[string]time.Time{}}
}

type rawConn struct {
	net.Conn
	reg   *rawConns
	key   string
	at    time.Time
	users map[string]struct{} // guarded by reg.mu
	once  sync.Once
}

func (c *rawConn) Close() error {
	c.once.Do(func() {
		c.reg.mu.Lock()
		if c.reg.m[c.key] == c {
			delete(c.reg.m, c.key)
		}
		c.reg.mu.Unlock()
	})
	return c.Conn.Close()
}

// admit records that the connection at addr carries a session of user, and reports
// whether it may: one opened before the user's last new password is closed instead.
// An address no connection is known at is let through: only the TCP transport is
// tracked, and it is the only one the lane serves (model.MieruTransportOr) — a UDP
// transport brought back would need its own way to end an old password's sessions.
func (r *rawConns) admit(addr, user string) bool {
	r.mu.Lock()
	c := r.m[addr]
	if c == nil {
		r.mu.Unlock()
		return true
	}
	if at, ok := r.rekeyed[user]; ok && c.at.Before(at) {
		r.mu.Unlock()
		_ = c.Close()
		return false
	}
	c.users[user] = struct{}{}
	r.mu.Unlock()
	return true
}

// rekey notes that user has a new password and closes the connections that carried
// their sessions: whatever is open on the old one goes, and nothing else.
func (r *rawConns) rekey(user string) {
	r.mu.Lock()
	r.rekeyed[user] = time.Now()
	var gone []*rawConn
	for _, c := range r.m {
		if _, ok := c.users[user]; ok {
			gone = append(gone, c)
		}
	}
	r.mu.Unlock()
	for _, c := range gone {
		_ = c.Close()
	}
}

// forget drops a user's rekey time once no connection open is older than it: none can
// then hold the old password. Kept until then whatever the user list says — a user
// disabled and let back in on the same new password is no rekey to Apply, and a
// connection from before it must still be refused.
func (r *rawConns) forget() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rekeyed) == 0 {
		return
	}
	var oldest time.Time
	for _, c := range r.m {
		if oldest.IsZero() || c.at.Before(oldest) {
			oldest = c.at
		}
	}
	for name, at := range r.rekeyed {
		if oldest.IsZero() || !oldest.Before(at) {
			delete(r.rekeyed, name)
		}
	}
}

// listenerFactory is the multiplexer's own TCP listener, with each connection it
// accepts tracked in reg.
type listenerFactory struct {
	base apicommon.StreamListenerFactory
	reg  *rawConns
}

func newListenerFactory(reg *rawConns) listenerFactory {
	return listenerFactory{base: &net.ListenConfig{Control: sockopts.DefaultListenerControl()}, reg: reg}
}

func (f listenerFactory) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	l, err := f.base.Listen(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &trackedListener{Listener: l, reg: f.reg}, nil
}

type trackedListener struct {
	net.Listener
	reg *rawConns
}

func (l *trackedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	rc := &rawConn{Conn: c, reg: l.reg, key: c.RemoteAddr().String(), at: time.Now(), users: map[string]struct{}{}}
	l.reg.mu.Lock()
	l.reg.m[rc.key] = rc
	l.reg.mu.Unlock()
	return rc, nil
}
