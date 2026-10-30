package model

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
)

// A WireGuard inbound is the lane for a network that lets through nothing but a
// whitelist. It is never reached directly: the user's TURN client (Free Turn Proxy, VK
// Turn Proxy) takes credentials from a VK call invite link and has VK's TURN servers
// relay WireGuard — in DTLS, masked as a call's audio — to Port. The panel's relay
// (internal/turnrelay) takes the mask and the DTLS off and hands each packet to Xray's
// WireGuard inbound on loopback at WGLocalPort. From there it is an inbound like any
// other: users by email, routing, stats.
//
// Users are peers by the tunnel identity they already have for AmneziaWG — the key in
// users.wg_private_key and the address their slot gives them — so this lane needs no
// new per-user secret either (see the note on the protocol constants).

// TurnClientListen is where a vk-turn-proxy client listens by default, and so the
// endpoint of the WireGuard config a user imports: the tunnel goes to the TURN client on
// the same device, which carries it to the relay.
const TurnClientListen = "127.0.0.1:9000"

// TurnLinkMaxLen bounds a stored call link. Real ones are well under a hundred.
const TurnLinkMaxLen = 300

// turnLinkHosts are the hosts a call link may be on. VK only: every client the panel
// hands this lane to — VK Turn Proxy on iOS, Free Turn Proxy on Android and computers —
// takes TURN credentials from VK calls and from nothing else.
var turnLinkHosts = map[string]bool{
	"vk.com": true, "m.vk.com": true, "vk.ru": true, "m.vk.ru": true, "vk.me": true,
}

// ValidTurnLink reports whether link is a VK call link.
func ValidTurnLink(link string) bool {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	return turnLinkHosts[strings.ToLower(u.Hostname())]
}

// The other WireGuard inbound, InbWireGuard, is the same Xray inbound without any of
// that: it listens on its public Port itself, so a stock WireGuard client reaches it
// directly (Endpoint = host:Port) and there is no relay, mask, call link or loopback
// port. The users' tunnel identities and everything downstream are shared.

// normalizeWireGuard is Normalize for a WireGuard inbound: the transport and security
// are fixed, and every field that belongs to another protocol is dropped.
func (in *Inbound) normalizeWireGuard() {
	o := &in.Opts
	o.Security = SecNone
	o.WGPrivateKey = strings.TrimSpace(o.WGPrivateKey)
	o.WGPublicKey = strings.TrimSpace(o.WGPublicKey)
	if in.Protocol == InbWireGuard {
		o.Transport = TrWireGuard
		// Nothing of the relay's: the public port is Xray's own.
		o.WGLocalPort, o.TurnLink, o.TurnMaskKey = 0, "", ""
	} else {
		o.Transport = TrTURN
		o.TurnLink = strings.TrimSpace(o.TurnLink)
		o.TurnMaskKey = strings.ToLower(strings.TrimSpace(o.TurnMaskKey))
	}
	o.SNI, o.FP, o.Path, o.Host, o.Mode, o.ServiceName, o.Flow = "", "", "", "", "", "", ""
	o.RealityDest, o.RealityPrivateKey, o.RealityPublicKey, o.RealityShortID = "", "", "", ""
	o.RealityMaxTimeDiff = 0
	o.XHTTPExtra, o.Sockopt, o.TLSExtra = nil, nil, nil
	o.HeaderType, o.HeaderHosts, o.HeaderPaths = "", nil, nil
	o.Authority, o.MultiMode = "", false
	o.HopStart, o.HopEnd, o.HopInterval, o.Obfs = 0, 0, "", ""
	o.Method, o.ShadowKey = "", ""
}

// clearWireGuard drops the WireGuard fields from an inbound of another protocol.
func (o *InboundOpts) clearWireGuard() {
	o.WGPrivateKey, o.WGPublicKey, o.WGLocalPort, o.TurnLink, o.TurnMaskKey = "", "", 0, "", ""
}

// NeedsWireGuardKey reports whether a WireGuard inbound still needs its server key.
func (in *Inbound) NeedsWireGuardKey() bool {
	return IsWireGuard(in.Protocol) && !validWGKey(in.Opts.WGPrivateKey)
}

// TurnMaskProfile is the Free Turn Proxy wire profile the panel hands its clients: the
// fullest imitation of a WebRTC audio stream. The relay reads every profile, so this
// only decides what the links ask for.
const TurnMaskProfile = "rtpopus3"

// NeedsTurnMaskKey reports whether a WireGuard inbound still needs its masking key.
func (in *Inbound) NeedsTurnMaskKey() bool {
	return in.Protocol == InbTurnVK && !validMaskKey(in.Opts.TurnMaskKey)
}

// validMaskKey reports whether s is a masking key: hex of 32 bytes.
func validMaskKey(s string) bool {
	raw, err := hex.DecodeString(s)
	return err == nil && len(raw) == 32
}

// validateWireGuard is Validate for a WireGuard inbound.
func (in *Inbound) validateWireGuard() error {
	o := in.Opts
	if !validWGKey(o.WGPrivateKey) || !validWGKey(o.WGPublicKey) {
		return fieldErr("err.wgKeyBad", "ключ WireGuard подключения отсутствует или повреждён")
	}
	if in.Protocol == InbWireGuard {
		return nil
	}
	if !validMaskKey(o.TurnMaskKey) {
		return fieldErr("err.wgKeyBad", "ключ WireGuard подключения отсутствует или повреждён")
	}
	if o.WGLocalPort < 1 || o.WGLocalPort > 65535 || o.WGLocalPort == in.Port {
		return fieldErr("err.wgLocalPortBad", "внутренний порт WireGuard вне диапазона или совпадает с портом relay")
	}
	if o.TurnLink != "" && (len(o.TurnLink) > TurnLinkMaxLen || !ValidTurnLink(o.TurnLink)) {
		return fieldErr("err.turnLinkBad", "ссылка на звонок: нужна https-ссылка на звонок ВКонтакте (vk.com/call/join/…)")
	}
	return nil
}

// validWGKey reports whether s is a WireGuard key as its tools write one: base64 of 32
// bytes.
func validWGKey(s string) bool {
	raw, err := base64.StdEncoding.DecodeString(s)
	return err == nil && len(raw) == 32
}
