package xray

import (
	"encoding/json"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Removing a user through the API only refuses their next connection: a VLESS or Trojan
// connection authenticates once, when it opens, and Xray leaves whatever is already open
// running. A disabled user — or one out of traffic — kept a download going for as long
// as it lasted. So every process keeps, from its access log, the client end of each TCP
// connection a user opened, and removing the user closes those sockets through the
// kernel (see sockdiag_linux.go). Nobody else's connection is touched, and Xray is not
// restarted.
//
// The access log has no line for a connection closing, so the addresses of closed ones
// are dropped by sweeping against the kernel's socket table once there are enough of
// them to matter.

// connEnd is one side of a connection as the kernel lists it: the client's address and
// the local port it connected to.
type connEnd struct {
	client netip.AddrPort
	port   uint16
}

type connRef struct {
	client netip.AddrPort
	tag    string // the inbound the connection came in on
}

const (
	// connTrackSweepAt is how many remembered connections make the next access line
	// start a sweep, at most once per connTrackSweepEvery.
	connTrackSweepAt    = 4096
	connTrackSweepEvery = time.Minute
	// connTrackMax bounds the table when sweeps cannot run (no sock_diag): past it the
	// table starts over rather than grow without end.
	connTrackMax = 1 << 17
)

// connTracker remembers, per user, the TCP connections the process accepted for them.
type connTracker struct {
	mu sync.Mutex
	// ports is each tracked inbound's listening port, from the config the process runs;
	// public the port its clients connect to instead, where a front holds that one (the
	// TCP-TLS lane). Kept apart so neither update undoes the other.
	ports   map[string]uint16
	public  map[string]uint16
	users   map[string]map[connRef]int64 // email → connection → when it was logged (unix nanos)
	owner   map[connRef]string           // whose a connection was last logged as
	n       int
	sweptAt time.Time

	sweeping atomic.Bool
}

func newConnTracker(ports, public map[string]uint16) *connTracker {
	return &connTracker{ports: ports, public: public, users: map[string]map[connRef]int64{}, owner: map[connRef]string{}}
}

// portOf is the local port a tracked inbound's client sockets sit on. Caller holds mu.
func (t *connTracker) portOf(tag string) (uint16, bool) {
	p, ok := t.ports[tag]
	if !ok {
		return 0, false
	}
	if q, ok := t.public[tag]; ok {
		return q, true
	}
	return p, true
}

// forget drops one remembered connection. Caller holds mu.
func (t *connTracker) forget(email string, ref connRef) {
	set := t.users[email]
	if _, ok := set[ref]; !ok {
		return
	}
	delete(set, ref)
	t.n--
	if len(set) == 0 {
		delete(t.users, email)
	}
	if t.owner[ref] == email {
		delete(t.owner, ref)
	}
}

// tcpUserPorts maps each inbound whose TCP connections each belong to one client —
// VLESS, Trojan and Shadowsocks on plain TCP, or anything under REALITY — to its port.
// Hysteria2 and WireGuard ride UDP and are cut their own way. WebSocket, gRPC, XHTTP and
// HTTPUpgrade are left out unless REALITY rules a CDN out: behind one, a single
// connection can carry many users, and closing it for one would cut off the rest.
func tcpUserPorts(cfg []byte) map[string]uint16 {
	var doc struct {
		Inbounds []struct {
			Tag            string          `json:"tag"`
			Port           json.RawMessage `json:"port"`
			Protocol       string          `json:"protocol"`
			StreamSettings struct {
				Network  string `json:"network"`
				Security string `json:"security"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	out := map[string]uint16{}
	if json.Unmarshal(cfg, &doc) != nil {
		return out
	}
	for _, in := range doc.Inbounds {
		switch in.Protocol {
		case "vless", "trojan", "shadowsocks":
		default:
			continue
		}
		switch in.StreamSettings.Network {
		case "", "tcp", "raw":
		default:
			if in.StreamSettings.Security != "reality" {
				continue
			}
		}
		p, err := strconv.ParseUint(strings.Trim(string(in.Port), `"`), 10, 16)
		if err != nil || p == 0 || in.Tag == "" {
			continue
		}
		out[in.Tag] = uint16(p)
	}
	return out
}

// observe records the connection an access line reports for email.
func (t *connTracker) observe(email, line string) {
	if t == nil {
		return
	}
	client, ok := accessClient(line)
	if !ok {
		return
	}
	tag := accessInbound(line)
	now := time.Now()
	t.mu.Lock()
	if _, ok := t.ports[tag]; !ok {
		t.mu.Unlock()
		return
	}
	// Copies, so the map does not keep every access line it was read from alive.
	ref := connRef{client: client, tag: strings.Clone(tag)}
	// One client address, one owner: a CGNAT address that another user reuses is theirs
	// now, and removing the first user must not close it.
	if prev, ok := t.owner[ref]; ok && prev != email {
		t.forget(prev, ref)
	}
	set := t.users[email]
	if set == nil {
		email = strings.Clone(email)
		set = map[connRef]int64{}
		t.users[email] = set
	}
	if _, seen := set[ref]; !seen {
		t.n++
		t.owner[ref] = strings.Clone(email)
	}
	set[ref] = now.UnixNano()
	sweep := t.n >= connTrackSweepAt && now.Sub(t.sweptAt) >= connTrackSweepEvery
	t.mu.Unlock()
	if sweep && t.sweeping.CompareAndSwap(false, true) {
		go t.sweep()
	}
}

// sweep forgets the connections that are no longer open. One logged after the socket
// table was read is kept whatever the table says: it may simply be newer than the read.
func (t *connTracker) sweep() {
	defer t.sweeping.Store(false)
	start := time.Now()
	socks, err := dumpTCP()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweptAt = time.Now()
	if err != nil {
		if t.n > connTrackMax {
			slog.Warn("xray: forgetting remembered connections, the socket table cannot be read", "count", t.n, "err", err)
			t.users, t.owner, t.n = map[string]map[connRef]int64{}, map[connRef]string{}, 0
		}
		return
	}
	open := make(map[connEnd]struct{}, len(socks))
	for _, s := range socks {
		open[connEnd{s.remote, s.local.Port()}] = struct{}{}
	}
	for email, set := range t.users {
		for ref, at := range set {
			if at >= start.UnixNano() {
				continue
			}
			port, _ := t.portOf(ref.tag)
			if _, ok := open[connEnd{ref.client, port}]; !ok {
				t.forget(email, ref)
			}
		}
	}
}

// setPorts replaces the listening ports with those of the config the process now runs:
// an inbound added live is one more to remember connections on.
func (t *connTracker) setPorts(ports map[string]uint16) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.ports = ports
	t.mu.Unlock()
}

// setPublic replaces the ports clients connect to where a front holds them.
func (t *connTracker) setPublic(public map[string]uint16) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.public = public
	t.mu.Unlock()
}

// takeInbounds removes and returns every connection on the given inbounds.
func (t *connTracker) takeInbounds(tags []string) map[connEnd]struct{} {
	if t == nil || len(tags) == 0 {
		return nil
	}
	gone := make(map[string]bool, len(tags))
	for _, tag := range tags {
		gone[tag] = true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[connEnd]struct{}{}
	for email, set := range t.users {
		for ref := range set {
			if !gone[ref.tag] {
				continue
			}
			port, _ := t.portOf(ref.tag)
			out[connEnd{ref.client, port}] = struct{}{}
			t.forget(email, ref)
		}
	}
	return out
}

// take removes and returns the connections of the given users on the given inbounds —
// every inbound when tags is nil.
func (t *connTracker) take(emails []string, tags map[string]bool) map[connEnd]struct{} {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[connEnd]struct{}{}
	for _, email := range emails {
		for ref := range t.users[email] {
			if tags != nil && !tags[ref.tag] {
				continue
			}
			port, _ := t.portOf(ref.tag)
			out[connEnd{ref.client, port}] = struct{}{}
			t.forget(email, ref)
		}
	}
	return out
}

// cutConns closes the open TCP connections of the given users on the given inbounds
// (every inbound when tags is nil) of the running process. Best effort: a failure is
// logged, never returned — the users are already out of the config, and what is left
// open is what the panel could not end before.
func (s *Supervisor) cutConns(emails []string, tags []string) {
	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	if p == nil || len(emails) == 0 {
		return
	}
	var tagSet map[string]bool
	if tags != nil {
		tagSet = make(map[string]bool, len(tags))
		for _, tag := range tags {
			tagSet[tag] = true
		}
	}
	if n := closeConns(p.conns.take(emails, tagSet)); n > 0 {
		slog.Info("xray: closed removed users' open connections", "users", len(emails), "connections", n)
	}
}

// cutInboundConns closes every connection still open on inbounds the running process
// no longer has.
func (s *Supervisor) cutInboundConns(tags []string) {
	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	if p == nil {
		return
	}
	if n := closeConns(p.conns.takeInbounds(tags)); n > 0 {
		slog.Info("xray: closed the connections of removed inbounds", "inbounds", len(tags), "connections", n)
	}
}

// closeConns closes the open sockets among targets and returns how many it closed.
func closeConns(targets map[connEnd]struct{}) int {
	if len(targets) == 0 {
		return 0
	}
	socks, err := dumpTCP()
	if err != nil {
		slog.Warn("xray: cannot close connections", "count", len(targets), "err", err)
		return 0
	}
	var hit []tcpSock
	for _, sk := range socks {
		if _, ok := targets[connEnd{sk.remote, sk.local.Port()}]; ok {
			hit = append(hit, sk)
		}
	}
	n, err := destroyTCP(hit)
	if err != nil {
		slog.Warn("xray: closing connections failed", "closed", n, "of", len(hit), "err", err)
	}
	return n
}

// accessClient is the client end of an access line's TCP connection. UDP (Hysteria2,
// Shadowsocks over UDP) has no socket to close; loopback is a panel hop; port 0 is an
// address taken from a forwarding header, not the socket's.
func accessClient(line string) (netip.AddrPort, bool) {
	f := strings.Index(line, "from ")
	if f < 0 {
		return netip.AddrPort{}, false
	}
	tok := line[f+len("from "):]
	if sp := strings.IndexByte(tok, ' '); sp > 0 {
		tok = tok[:sp]
	}
	if strings.HasPrefix(tok, "udp:") {
		return netip.AddrPort{}, false
	}
	ap, err := netip.ParseAddrPort(strings.TrimPrefix(tok, "tcp:"))
	if err != nil || ap.Port() == 0 {
		return netip.AddrPort{}, false
	}
	ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
	if ap.Addr().IsLoopback() {
		return netip.AddrPort{}, false
	}
	return ap, true
}

// accessInbound is the inbound tag of an access line's route: the bracket after the
// destination, "[in >> out]", "[in -> out]" or "[in ==> out]" depending on how the rule
// matched. Searched for past the destination, whose IPv6 form has brackets of its own.
func accessInbound(line string) string {
	a := strings.Index(line, " accepted ")
	if a < 0 {
		return ""
	}
	rest := line[a+len(" accepted "):]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return ""
	}
	rest = rest[sp:]
	open, closing := strings.Index(rest, " ["), strings.IndexByte(rest, ']')
	if open < 0 || closing < open {
		return ""
	}
	in := rest[open+2 : closing]
	for _, sep := range []string{" >> ", " -> ", " ==> "} {
		if tag, _, ok := strings.Cut(in, sep); ok {
			return tag
		}
	}
	return in
}
