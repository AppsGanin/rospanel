// Package vlessin is how a lane the panel answers itself — NaiveProxy, mieru — hands a
// tunnel to Xray: a connection to the lane's internal VLESS inbound (an abstract unix
// socket or a loopback port, see xray/inner.go), a PROXY line naming the client so the
// access log and everything that reads it see the real address, and a VLESS request
// for the target on the user's UUID. Xray then does the stats, routing, device limits
// and speed caps as for any lane.
package vlessin

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/netip"
	"strings"
)

// VLESS request commands.
const (
	CmdTCP = 1
	CmdUDP = 2
)

// Network is the network an inner address is dialed on: "unix" for an abstract socket
// ("@name"), "tcp" for a loopback host:port.
func Network(addr string) string {
	if strings.HasPrefix(addr, "@") {
		return "unix"
	}
	return "tcp"
}

// ParseUUID turns the textual UUID into the 16 bytes VLESS sends.
func ParseUUID(s string) ([16]byte, bool) {
	var id [16]byte
	h := strings.ReplaceAll(s, "-", "")
	if len(h) != 32 {
		return id, false
	}
	if _, err := hex.Decode(id[:], []byte(h)); err != nil {
		return id, false
	}
	return id, true
}

// AppendRequest appends a VLESS request header to host:port: version 0, the user's id,
// no addons (the inner inbounds' clients carry no flow), the command.
func AppendRequest(b []byte, id [16]byte, cmd byte, host string, port uint16) []byte {
	b = append(b, 0)
	b = append(b, id[:]...)
	b = append(b, 0, cmd)
	b = binary.BigEndian.AppendUint16(b, port)
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Is4() || ip.Is4In6() {
			a := ip.Unmap().As4()
			return append(append(b, 1), a[:]...)
		}
		a := ip.As16()
		return append(append(b, 3), a[:]...)
	}
	b = append(b, 2, byte(len(host)))
	return append(b, host...)
}

// ReadResponse consumes the VLESS response header: version, addons length, addons.
func ReadResponse(r io.Reader) bool {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil || hdr[0] != 0 {
		return false
	}
	if hdr[1] > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(hdr[1])); err != nil {
			return false
		}
	}
	return true
}

// ProxyHeader is a PROXY v1 line naming the client (ip:port), so the inbound logs — and
// the access log's consumers act on — the address the tunnel really came from. The
// destination is a placeholder of the same family: Xray reads only the source.
func ProxyHeader(remote string) []byte {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return []byte("PROXY UNKNOWN\r\n") // no address to name; Xray keeps the socket's
	}
	ip := ap.Addr().Unmap().WithZone("") // a zone is no part of the PROXY grammar
	if ip.Is4() {
		return fmt.Appendf(nil, "PROXY TCP4 %s 127.0.0.1 %d 443\r\n", ip, ap.Port())
	}
	return fmt.Appendf(nil, "PROXY TCP6 %s ::1 %d 443\r\n", ip, ap.Port())
}
