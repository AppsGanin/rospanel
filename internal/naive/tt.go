package naive

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/AppsGanin/rospanel/internal/vlessin"
)

// TrustTunnel (github.com/TrustTunnel/TrustTunnel, PROTOCOL.md, version 1.0) over HTTP/2:
//
//   - TCP: CONNECT host:port, one stream per connection — what NaiveProxy sends, without
//     the padding;
//   - health check: CONNECT _check, answered 200 with the stream ended;
//   - UDP: CONNECT _udp2, one stream carrying every datagram of the session. A client
//     packet is [len uint32][src 16][src port 2][dst 16][dst port 2][app len 1][app]
//     [payload], len counting what follows it, an IPv4 address as 12 zero bytes and its
//     4; a reply is the same with source and destination swapped and no app name;
//   - ICMP: CONNECT _icmp, echo requests multiplexed the same way.
//
// The client opens _udp2 again when the stream ends, so the lane's idle bound holds.
// Each 4-tuple is one VLESS UDP connection on trusttunnel-in, as for NaiveProxy's UDP
// over TCP, so Xray counts, routes and logs every datagram. ICMP is not carried: Xray
// has no way to send it, and a ping sent past Xray would skip its routing. The stream
// is accepted and its requests read and dropped, so a ping times out and nothing else
// of the session breaks.

type ttKind int

const (
	ttTCP ttKind = iota
	ttCheck
	ttUDP
	ttICMP
)

// ttTarget parses a TrustTunnel CONNECT authority: a pseudo-host (with or without the
// port 0 the client gives it) or host:port.
func ttTarget(authority string) (string, uint16, ttKind, error) {
	name := authority
	if h, p, err := net.SplitHostPort(authority); err == nil && p == "0" {
		name = h
	}
	switch name {
	case "_check":
		return "", 0, ttCheck, nil
	case "_udp2":
		return "", 0, ttUDP, nil
	case "_icmp":
		return "", 0, ttICMP, nil
	}
	host, p, err := net.SplitHostPort(authority)
	if err != nil {
		return "", 0, ttTCP, err
	}
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil || port == 0 {
		return "", 0, ttTCP, fmt.Errorf("bad port %q", p)
	}
	if host == "" || len(host) > 255 || strings.HasPrefix(host, "_") {
		return "", 0, ttTCP, fmt.Errorf("bad host %q", host)
	}
	return host, uint16(port), ttTCP, nil
}

const (
	ttAddrLen = 16
	// ttHeader is a client packet's fixed part after its length: two addresses and
	// ports, and the app name's length.
	ttHeader = 2*(ttAddrLen+2) + 1
	// ttMaxPacket bounds a client packet: a UDP payload (a VLESS frame's length is 16
	// bits), the fixed part and the longest app name.
	ttMaxPacket = ttHeader + 255 + 0xffff
	// ttMaxUDPDests is maxUDPDests for a TrustTunnel stream, which carries every UDP
	// flow of a device rather than one application's.
	ttMaxUDPDests = 256
	// ttICMPRequest is an echo request's size: id, destination, sequence, TTL, size.
	ttICMPRequest = 2 + ttAddrLen + 2 + 1 + 2
)

// ttAddr reads a 16-byte TrustTunnel address and its port.
func ttAddr(b []byte) netip.AddrPort {
	a := netip.AddrFrom16([16]byte(b[:ttAddrLen]))
	if [12]byte(b[:12]) == [12]byte{} {
		a = netip.AddrFrom4([4]byte(b[12:ttAddrLen]))
	}
	return netip.AddrPortFrom(a, binary.BigEndian.Uint16(b[ttAddrLen:]))
}

// putTTAddr writes an address and port the way ttAddr reads them.
func putTTAddr(b []byte, ap netip.AddrPort) {
	a := ap.Addr()
	if a.Is4() || a.Is4In6() {
		clear(b[:12])
		v4 := a.Unmap().As4()
		copy(b[12:ttAddrLen], v4[:])
	} else {
		v6 := a.As16()
		copy(b[:ttAddrLen], v6[:])
	}
	binary.BigEndian.PutUint16(b[ttAddrLen:], ap.Port())
}

// ttFlow is one UDP "connection" of a TrustTunnel stream: the client's socket and the
// destination.
type ttFlow struct{ src, dst netip.AddrPort }

// ttDest is the VLESS connection of one flow.
type ttDest struct {
	conn net.Conn
	used uint64      // when it last sent, in packets of the stream
	dead atomic.Bool // Xray ended the connection (idle timeout, a failed outbound)
}

// serveTTUDP demultiplexes a _udp2 stream onto one VLESS UDP connection per flow and
// multiplexes their replies back.
func (s *Server) serveTTUDP(r *http.Request, st *stream, body io.Reader, cw *clientWriter, u cred) {
	br := bufio.NewReaderSize(idleReader{r: body, st: st}, 16<<10)
	flows := map[ttFlow]*ttDest{}
	var wg sync.WaitGroup
	defer func() {
		for _, d := range flows {
			_ = d.conn.Close()
		}
		wg.Wait()
	}()
	var lenBuf [4]byte
	pkt := make([]byte, 2+1500) // [len uint16][payload] for VLESS; grows to the largest seen
	var hdr [ttHeader]byte
	var seq uint64
	for {
		if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
			return
		}
		total := binary.BigEndian.Uint32(lenBuf[:])
		if total > ttMaxPacket {
			return // not a client this lane can follow: end the stream
		}
		if total < ttHeader {
			if _, err := br.Discard(int(total)); err != nil {
				return
			}
			continue
		}
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return
		}
		f := ttFlow{src: ttAddr(hdr[:]), dst: ttAddr(hdr[ttAddrLen+2:])}
		rest := int(total) - ttHeader
		app := int(hdr[ttHeader-1])
		if app > rest {
			if _, err := br.Discard(rest); err != nil {
				return
			}
			continue
		}
		if _, err := br.Discard(app); err != nil {
			return
		}
		n := rest - app
		if n > 0xffff {
			if _, err := br.Discard(n); err != nil {
				return
			}
			continue
		}
		if len(pkt) < 2+n {
			pkt = append(pkt[:2], make([]byte, n)...)
		}
		binary.BigEndian.PutUint16(pkt, uint16(n))
		if _, err := io.ReadFull(br, pkt[2:2+n]); err != nil {
			return
		}
		if !f.dst.IsValid() || f.dst.Port() == 0 {
			continue
		}
		d := flows[f]
		if d != nil && d.dead.Load() {
			// Xray let this flow go; a write into the closed connection would lose the
			// packet without an error. Open it again instead.
			_ = d.conn.Close()
			st.detach(d.conn)
			delete(flows, f)
			d = nil
		}
		if d == nil {
			if len(flows) >= ttMaxUDPDests {
				evictIdlestFlow(st, flows)
			}
			conn, err := s.dialXray(r, st, u, vlessin.CmdUDP, f.dst.Addr().Unmap().String(), f.dst.Port())
			if errors.Is(err, net.ErrClosed) {
				return // the stream has ended
			}
			if err != nil {
				continue // this packet is lost, as UDP may lose it
			}
			d = &ttDest{conn: conn}
			flows[f] = d
			wg.Add(1)
			go func() {
				defer wg.Done()
				ttReplies(d, f, cw)
			}()
		}
		seq++
		d.used = seq
		if _, err := d.conn.Write(pkt[:2+n]); err != nil {
			_ = d.conn.Close()
			st.detach(d.conn)
			delete(flows, f)
		}
	}
}

// evictIdlestFlow closes the flow that has gone longest without sending.
func evictIdlestFlow(st *stream, flows map[ttFlow]*ttDest) {
	var key ttFlow
	var idlest *ttDest
	for k, d := range flows {
		if idlest == nil || d.used < idlest.used {
			key, idlest = k, d
		}
	}
	if idlest != nil {
		_ = idlest.conn.Close()
		st.detach(idlest.conn)
		delete(flows, key)
	}
}

// ttReplies copies one flow's packets back to the client, from the destination to the
// client's socket.
func ttReplies(d *ttDest, f ttFlow, cw *clientWriter) {
	defer d.dead.Store(true)
	if !vlessin.ReadResponse(d.conn) {
		return
	}
	const head = 4 + 2*(ttAddrLen+2)
	out := make([]byte, head, head+1500) // grows to the largest packet seen
	putTTAddr(out[4:], f.dst)
	putTTAddr(out[4+ttAddrLen+2:], f.src)
	var l [2]byte
	for {
		if _, err := io.ReadFull(d.conn, l[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(l[:]))
		if cap(out) < head+n {
			grown := make([]byte, head, head+n)
			copy(grown, out[:head])
			out = grown
		}
		out = out[:head+n]
		if _, err := io.ReadFull(d.conn, out[head:]); err != nil {
			return
		}
		binary.BigEndian.PutUint32(out, uint32(head-4+n))
		if _, err := cw.Write(out); err != nil {
			return
		}
	}
}

// serveTTICMP reads an _icmp stream's echo requests and drops them (see above) until
// the client ends it or goes quiet for udpIdle.
func serveTTICMP(st *stream, body io.Reader) {
	buf := make([]byte, 64*ttICMPRequest)
	r := idleReader{r: body, st: st}
	for {
		if _, err := r.Read(buf); err != nil {
			return
		}
	}
}
