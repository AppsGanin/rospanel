package naive

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AppsGanin/rospanel/internal/vlessin"
)

// UDP over TCP, as sing-box's naive outbound speaks it (github.com/sagernet/sing,
// common/uot). The client CONNECTs to a magic host instead of a destination and sends
// UDP packets inside the stream:
//
//   - version 2 ("sp.v2.udp-over-tcp.arpa") opens with a request — one byte, whether the
//     stream is "connected" to one destination, then that destination as a SOCKS5
//     address (family 01/03/04, address, port);
//   - connected, every packet both ways is [len uint16][payload];
//   - otherwise each packet carries its own address — [address][len uint16][payload],
//     families 00 IPv4, 01 IPv6, 02 domain — and a reply carries the address it came
//     from;
//   - version 1 ("sp.udp-over-tcp.arpa") has no request and is always the latter.
//
// VLESS frames UDP the same way, [len][payload] after its header, so each destination
// is one VLESS UDP connection on naive-in: Xray still counts, routes and logs the
// packets, and a connected stream is a straight pipe.

type uotVersion int

const (
	uotNone uotVersion = iota
	uotV1
	uotV2
)

const (
	uotMagic       = "sp.v2.udp-over-tcp.arpa"
	uotMagicLegacy = "sp.udp-over-tcp.arpa"
)

// udpIdle ends a UDP tunnel the client has sent nothing on for this long — sing-box's
// own UDP timeout — so a stream opened and left idle does not hold its buffers forever.
var udpIdle = 5 * time.Minute

// idleReader refreshes the stream's read deadline before every read: a client quiet for
// udpIdle has its read fail, and the tunnel ends.
type idleReader struct {
	r  io.Reader
	st *stream
}

func (ir idleReader) Read(p []byte) (int, error) {
	ir.st.deadline(false, time.Now().Add(udpIdle))
	return ir.r.Read(p)
}

// maxUDPDests bounds the VLESS connections one stream holds in packet mode. A browser's
// QUIC or a DNS lookup talks to one or two destinations; a peer-to-peer call to a few
// dozen. Past the bound the destination idle longest is closed to make room.
const maxUDPDests = 64

// addrFamilies are the family bytes of one address encoding.
type addrFamilies struct{ v4, v6, fqdn byte }

var (
	socksFamilies  = addrFamilies{v4: 0x01, v6: 0x04, fqdn: 0x03} // the v2 request
	packetFamilies = addrFamilies{v4: 0x00, v6: 0x01, fqdn: 0x02} // per packet
)

var errBadFamily = errors.New("naive: unknown address family in UDP over TCP")

// readAddr reads an address and port in the given encoding, returning them and the bytes
// they were read from (a reply is addressed with the very same bytes).
func readAddr(r *bufio.Reader, f addrFamilies) (host string, port uint16, raw []byte, err error) {
	fam, err := r.ReadByte()
	if err != nil {
		return "", 0, nil, err
	}
	raw = []byte{fam}
	var n int
	switch fam {
	case f.v4:
		n = 4
	case f.v6:
		n = 16
	case f.fqdn:
		l, err := r.ReadByte()
		if err != nil {
			return "", 0, nil, err
		}
		raw = append(raw, l)
		n = int(l)
	default:
		return "", 0, nil, errBadFamily
	}
	b := make([]byte, n+2)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", 0, nil, err
	}
	raw = append(raw, b...)
	port = binary.BigEndian.Uint16(b[n:])
	switch fam {
	case f.v4:
		host = netip.AddrFrom4([4]byte(b[:4])).String()
	case f.v6:
		host = netip.AddrFrom16([16]byte(b[:16])).String()
	default:
		host = string(b[:n])
	}
	return host, port, raw, nil
}

// serveUDP runs a UDP-over-TCP stream.
func (s *Server) serveUDP(r *http.Request, st *stream, body io.Reader, cw *clientWriter, u cred, v uotVersion) {
	br := bufio.NewReaderSize(idleReader{r: body, st: st}, 4<<10)
	if v == uotV2 {
		connected, err := br.ReadByte()
		if err != nil {
			return
		}
		host, port, _, err := readAddr(br, socksFamilies)
		if err != nil {
			return
		}
		if connected != 0 {
			s.udpConnected(r, st, br, cw, u, host, port)
			return
		}
	}
	s.udpPackets(r, st, br, cw, u)
}

// udpConnected pipes a connected stream through one VLESS UDP connection: the framing
// is the same on both sides.
func (s *Server) udpConnected(r *http.Request, st *stream, br *bufio.Reader, cw *clientWriter, u cred, host string, port uint16) {
	conn, err := s.dialXray(r, st, u, vlessin.CmdUDP, host, port)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(conn, br)
		_ = conn.Close() // a UDP stream has no half-close: the client is done with it
	}()
	download(cw, conn)
	_ = conn.Close()
	_ = r.Body.Close()
	<-done
}

// udpDest is one destination of a packet-mode stream.
type udpDest struct {
	conn net.Conn
	addr []byte      // the address as the client wrote it, prefixed to every reply
	used uint64      // when it last sent, in packets of the stream
	dead atomic.Bool // Xray ended the connection (idle timeout, a failed outbound)
}

// udpPackets demultiplexes a packet-mode stream onto one VLESS UDP connection per
// destination, and multiplexes their replies back.
func (s *Server) udpPackets(r *http.Request, st *stream, br *bufio.Reader, cw *clientWriter, u cred) {
	dests := map[string]*udpDest{}
	var wg sync.WaitGroup
	defer func() {
		for _, d := range dests {
			_ = d.conn.Close()
		}
		wg.Wait()
	}()
	pkt := make([]byte, 2+1500) // grows to the largest packet seen
	var seq uint64
	for {
		host, port, raw, err := readAddr(br, packetFamilies)
		if err != nil {
			return
		}
		if _, err := io.ReadFull(br, pkt[:2]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(pkt[:2]))
		if len(pkt) < 2+n {
			pkt = append(pkt[:2], make([]byte, n)...)
		}
		if _, err := io.ReadFull(br, pkt[2:2+n]); err != nil {
			return
		}
		key := net.JoinHostPort(host, strconv.Itoa(int(port)))
		d := dests[key]
		if d != nil && d.dead.Load() {
			// Xray let this destination go; a write into the closed connection would
			// lose the packet without an error. Open it again instead.
			_ = d.conn.Close()
			st.detach(d.conn)
			delete(dests, key)
			d = nil
		}
		if d == nil {
			if len(dests) >= maxUDPDests {
				evictIdlest(st, dests)
			}
			conn, err := s.dialXray(r, st, u, vlessin.CmdUDP, host, port)
			if errors.Is(err, net.ErrClosed) {
				return // the stream has ended
			}
			if err != nil {
				continue // this packet is lost, as UDP may lose it
			}
			d = &udpDest{conn: conn, addr: raw}
			dests[key] = d
			wg.Add(1)
			go func() {
				defer wg.Done()
				udpReplies(d, cw)
			}()
		}
		seq++
		d.used = seq
		if _, err := d.conn.Write(pkt[:2+n]); err != nil {
			_ = d.conn.Close()
			st.detach(d.conn)
			delete(dests, key)
		}
	}
}

// evictIdlest closes the destination that has gone longest without sending.
func evictIdlest(st *stream, dests map[string]*udpDest) {
	var key string
	var idlest *udpDest
	for k, d := range dests {
		if idlest == nil || d.used < idlest.used {
			key, idlest = k, d
		}
	}
	if idlest != nil {
		_ = idlest.conn.Close()
		st.detach(idlest.conn)
		delete(dests, key)
	}
}

// udpReplies copies one destination's packets back to the client, each addressed with
// the destination's own address.
func udpReplies(d *udpDest, cw *clientWriter) {
	defer d.dead.Store(true)
	if !vlessin.ReadResponse(d.conn) {
		return
	}
	out := make([]byte, 0, len(d.addr)+2+1500) // grows to the largest packet seen
	for {
		out = append(out[:0], d.addr...)
		hdr := len(out)
		out = out[:hdr+2]
		if _, err := io.ReadFull(d.conn, out[hdr:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(out[hdr:]))
		out = slices.Grow(out, n)[:hdr+2+n]
		if _, err := io.ReadFull(d.conn, out[hdr+2:]); err != nil {
			return
		}
		if _, err := cw.Write(out); err != nil {
			return
		}
	}
}
