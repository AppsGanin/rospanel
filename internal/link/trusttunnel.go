package link

import (
	"encoding/base64"
	"net"
	"strconv"

	"github.com/AppsGanin/rospanel/internal/model"
)

// TrustTunnelOn reports whether a server hands out TrustTunnel: the lane is on, its
// certificate is one a client trusts, and the certificate's name is a domain. The client
// (TrustTunnelClient 1.1.11, core/src/vpn_manager.cpp) checks an IP certificate against
// the address with its port attached, which never matches, so a server known only by
// its IP has nothing the app could connect to. The link could pin a certificate, but a
// server's settings hold only its fingerprint, not the certificate a pin needs.
func TrustTunnelOn(set *model.Settings) bool {
	return set.TrustTunnelEnabled && !set.TLSInsecure && net.ParseIP(NaiveServerName(set)) == nil
}

// Deep link tags (TrustTunnel DEEP_LINK.md, version 2).
const (
	ttTagVersion  = 0x00
	ttTagHostname = 0x01
	ttTagAddress  = 0x02
	ttTagUsername = 0x05
	ttTagPassword = 0x06
	ttTagProtocol = 0x09
	ttTagName     = 0x0C

	ttLinkVersion = 2
	ttHTTP2       = 0x01
)

// TrustTunnel is a user's TrustTunnel deep link, tt://?<base64url TLV>: the server's
// certificate name to verify and send as SNI, the VLESS-TCP-TLS port where the decoy
// behind it answers the tunnel (internal/naive, tt.go), the lane's login and the
// user's UUID, HTTP/2 — :443 has no HTTP/3 of its own here — and the label to show.
func TrustTunnel(u model.User, set *model.Settings) string {
	var b []byte
	b = ttVarField(b, ttTagVersion, ttLinkVersion)
	b = ttField(b, ttTagHostname, NaiveServerName(set))
	b = ttField(b, ttTagAddress, net.JoinHostPort(set.Host, strconv.Itoa(set.VLESSPort)))
	b = ttField(b, ttTagUsername, model.TrustTunnelLogin(model.UserEmail(u.ID)))
	b = ttField(b, ttTagPassword, u.UUID)
	b = ttVarField(b, ttTagProtocol, ttHTTP2)
	b = ttField(b, ttTagName, LabelFor(model.ProtoTrustTunnel, u, set))
	return "tt://?" + base64.RawURLEncoding.EncodeToString(b)
}

// ttField appends a string field.
func ttField(b []byte, tag uint64, v string) []byte {
	b = appendQUICVarint(b, tag)
	b = appendQUICVarint(b, uint64(len(v)))
	return append(b, v...)
}

// ttVarField appends a field whose value is itself a varint.
func ttVarField(b []byte, tag, v uint64) []byte {
	val := appendQUICVarint(nil, v)
	b = appendQUICVarint(b, tag)
	b = appendQUICVarint(b, uint64(len(val)))
	return append(b, val...)
}

// appendQUICVarint appends v as an RFC 9000 §16 variable-length integer.
func appendQUICVarint(b []byte, v uint64) []byte {
	switch {
	case v < 1<<6:
		return append(b, byte(v))
	case v < 1<<14:
		return append(b, byte(v>>8)|0x40, byte(v))
	case v < 1<<30:
		return append(b, byte(v>>24)|0x80, byte(v>>16), byte(v>>8), byte(v))
	default:
		return append(b, byte(v>>56)|0xc0, byte(v>>48), byte(v>>40), byte(v>>32),
			byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
}
