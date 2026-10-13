package anytls

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"

	"github.com/AppsGanin/rospanel/internal/vlessin"
)

const (
	dialTimeout = 5 * time.Second
	// maxConnsPerUser bounds a user's open connections to Xray, across sessions.
	maxConnsPerUser = 1024
	// maxUDPDests bounds the destinations one UDP stream keeps a connection to Xray
	// for; past it the one quiet longest is closed.
	maxUDPDests = 64
)

// udpIdle ends a UDP stream the client has sent nothing on for this long.
var udpIdle = 5 * time.Minute

var errTooManyConns = errors.New("anytls: too many open tunnels for this user")

// target is a destination as VLESS addresses it.
func target(d M.Socksaddr) (string, uint16) {
	if d.Port == 0 {
		return "", 0
	}
	if d.IsDomain() {
		if len(d.Fqdn) > 255 {
			return "", 0
		}
		return d.Fqdn, d.Port
	}
	if !d.Addr.IsValid() {
		return "", 0
	}
	return d.Addr.Unmap().String(), d.Port
}

// isUoT reports whether a stream asks for sing-box's UDP over TCP (by its magic host).
func isUoT(d M.Socksaddr) bool {
	return d.Fqdn == uot.MagicAddress || d.Fqdn == uot.LegacyMagicAddress
}

// dialXray opens a VLESS connection on anytls-in: the PROXY line naming the client,
// then the request header.
func (s *Server) dialXray(u cred, source M.Socksaddr, cmd byte, host string, port uint16) (net.Conn, error) {
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
	req := vlessin.ProxyHeader(source.String())
	req = vlessin.AppendRequest(req, u.id, cmd, host, port)
	if _, err := conn.Write(req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// xrayConn gives its place in the user's count back once, on the first Close.
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

func (c *xrayConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// pipe carries a TCP stream both ways until Xray is done with it.
func pipe(client, xc net.Conn) error {
	up := make(chan struct{})
	go func() {
		defer close(up)
		_, _ = io.Copy(xc, client)
		if cw, ok := xc.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	var err error
	if vlessin.ReadResponse(xc) {
		_, err = io.Copy(client, xc)
	}
	_ = client.Close() // Xray is done: so is the stream, and with it the upload
	<-up
	return err
}

// udpDest is one destination of a UDP stream.
type udpDest struct {
	conn net.Conn
	addr M.Socksaddr
	used uint64
	dead atomic.Bool
}

// serveUoT carries sing-box's UDP over TCP: version 2 (the magic host
// sp.v2.udp-over-tcp.arpa) opens with a request naming whether the stream is
// connected and to where, version 1 has none and is not connected. Packets go out on
// one VLESS UDP connection per destination; replies come back addressed from it. It
// ends after udpIdle with nothing either way.
func (s *Server) serveUoT(conn net.Conn, u cred, source, destination M.Socksaddr) error {
	req := uot.Request{}
	if destination.Fqdn == uot.MagicAddress {
		r, err := uot.ReadRequest(conn)
		if err != nil {
			return err
		}
		req = *r
	}
	pc := uot.NewConn(conn, req)
	var writeMu sync.Mutex
	dests := map[string]*udpDest{}
	var wg sync.WaitGroup
	idle := time.AfterFunc(udpIdle, func() { _ = conn.Close() })
	defer func() {
		idle.Stop()
		_ = conn.Close()
		for _, d := range dests {
			_ = d.conn.Close()
		}
		wg.Wait()
	}()
	var seq uint64
	out := make([]byte, 0, 2+1500)
	for {
		b := buf.NewPacket()
		dest, err := pc.ReadPacket(b)
		if err != nil {
			b.Release()
			return nil
		}
		idle.Reset(udpIdle)
		host, port := target(dest)
		if host == "" {
			b.Release()
			continue
		}
		key := net.JoinHostPort(host, strconv.Itoa(int(port)))
		d := dests[key]
		if d != nil && d.dead.Load() {
			_ = d.conn.Close()
			delete(dests, key)
			d = nil
		}
		if d == nil {
			if len(dests) >= maxUDPDests {
				evictIdlest(dests)
			}
			xc, err := s.dialXray(u, source, vlessin.CmdUDP, host, port)
			if err != nil {
				b.Release()
				continue // this packet is lost, as UDP may lose it
			}
			d = &udpDest{conn: xc, addr: dest}
			dests[key] = d
			wg.Add(1)
			go func() {
				defer wg.Done()
				udpReplies(d, pc, &writeMu, func() { idle.Reset(udpIdle) })
			}()
		}
		seq++
		d.used = seq
		out = binary.BigEndian.AppendUint16(out[:0], uint16(b.Len()))
		out = append(out, b.Bytes()...)
		b.Release()
		if _, err := d.conn.Write(out); err != nil {
			_ = d.conn.Close()
			delete(dests, key)
		}
	}
}

func evictIdlest(dests map[string]*udpDest) {
	var key string
	var idlest *udpDest
	for k, d := range dests {
		if idlest == nil || d.used < idlest.used {
			key, idlest = k, d
		}
	}
	if idlest != nil {
		_ = idlest.conn.Close()
		delete(dests, key)
	}
}

// udpReplies copies one destination's packets back to the client; each one keeps the
// stream from idling out, as a stream that only receives is not idle.
func udpReplies(d *udpDest, pc *uot.Conn, writeMu *sync.Mutex, alive func()) {
	defer d.dead.Store(true)
	if !vlessin.ReadResponse(d.conn) {
		return
	}
	var hdr [2]byte
	for {
		if _, err := io.ReadFull(d.conn, hdr[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[:]))
		b := buf.NewSize(n)
		if _, err := b.ReadFullFrom(d.conn, n); err != nil {
			b.Release()
			return
		}
		writeMu.Lock()
		err := pc.WritePacket(b, d.addr) // takes the buffer
		writeMu.Unlock()
		if err != nil {
			return
		}
		alive()
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
