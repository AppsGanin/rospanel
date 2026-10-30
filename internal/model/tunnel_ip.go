package model

import "strings"

// TunnelIPLike is an SQL LIKE pattern matching every address of the tunnel subnet the
// panel hands its WireGuard users (awg.Subnet, 10.66.0.0/16; awg's tests hold the two
// together).
//
// A WireGuard inbound behind Xray cannot see where its client really is: the access log
// names the peer's tunnel address, which is the same for every device the user has and
// is not an address of any machine. It is stored as the connection's address so the user
// shows online, but it must not be counted as a device — beside the real address the
// same person has on another protocol it made two of one.
const TunnelIPLike = "10.66.%"

// TunnelProtoLabels are the names a WireGuard inbound's protocol goes by on a connection
// row. Brand-style names, as AmneziaWG's: not translated. AmneziaWG itself is not here:
// its engine sees the peer's real address, which is recorded as it is.
var TunnelProtoLabels = map[string]string{InbWireGuard: "WG", InbTurnVK: "TURN-VK"}

// TunnelVia lists, in a fixed order, the protocol labels of the WireGuard inbounds in
// list that access lets a user onto.
func TunnelVia(list []Inbound, access Access) []string {
	var out []string
	for _, p := range []string{InbWireGuard, InbTurnVK} {
		for _, in := range list {
			if in.Protocol == p && in.Enabled && access.AllowsInbound(in.ID) {
				out = append(out, TunnelProtoLabels[p])
				break
			}
		}
	}
	return out
}

// IsTunnelIP reports whether a recorded connection address is a WireGuard tunnel one.
func IsTunnelIP(ip string) bool {
	return strings.HasPrefix(ip, strings.TrimSuffix(TunnelIPLike, "%"))
}
