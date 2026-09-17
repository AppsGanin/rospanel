// Package sub builds the per-user subscription: the machine payload consumed by
// VPN clients and the human-facing page (QR + one-tap import buttons).
package sub

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/extsub"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// ShareLinks returns one server's links for a user, in client-import order: the
// enabled built-in lanes first, then each custom inbound in its display order.
// Protocols switched off in the Connections panel are omitted.
func ShareLinks(u model.User, srv Server) []string {
	set := srv.Set
	links := make([]string, 0, 3+len(srv.Custom))
	if set.VLESSEnabled && srv.allowsBuiltin(model.LaneVLESS) {
		links = append(links, link.VLESS(u, set))
	}
	// A REALITY lane with no public key cannot be dialled: the key is what the client
	// authenticates the handshake with, and the panel mints it when the lane is first
	// switched on. A node added before its keys landed would otherwise hand out a link
	// with an empty pbk — one that fails with no message a user could act on.
	if set.RealityEnabled && set.RealityPublicKey != "" && srv.allowsBuiltin(model.LaneReality) {
		links = append(links, link.Reality(u, set))
	}
	if set.HysteriaEnabled && srv.allowsBuiltin(model.LaneHysteria) {
		links = append(links, link.Hysteria2(u, set))
	}
	for _, in := range srv.Custom {
		if !srv.allowsInbound(in.ID) {
			continue
		}
		if l := link.Custom(u, in, set); l != "" {
			links = append(links, l)
		}
	}
	// External servers go last and as received: the link is theirs, the label is
	// theirs, only the choice of who gets it is ours.
	for _, e := range srv.externalEndpoints() {
		links = append(links, e.Link)
	}
	return links
}

// ShareLinksAll concatenates the links for a user across every server — the local
// one plus each enabled node — so a subscription carries one entry per lane × server.
// Each settings clone carries its own host/ports/keys and a NodeLabel that
// disambiguates the links.
func ShareLinksAll(u model.User, servers []Server) []string {
	var links []string
	for _, srv := range servers {
		links = append(links, ShareLinks(u, srv)...)
	}
	return links
}

// Base64Payload is the universal v2ray-style subscription body: the links joined
// by newlines, base64-encoded. Consumed by v2rayNG, Hiddify, Streisand, NekoBox,
// Shadowrocket, etc.
func Base64Payload(links []string) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
}

// URL is the public subscription URL for a token (always https on the host).
func URL(set *model.Settings, token string) string {
	return "https://" + set.Host + "/" + set.SubPathOr() + "/" + token
}

// DeepLink is one "open in client" button. Href is template.URL so html/template
// keeps the custom client schemes (happ://, v2rayng://, …) instead of sanitizing
// them to "#ZgotmplZ". Platform notes which OS the client targets.
type DeepLink struct {
	Label    string
	Platform string
	Href     template.URL
}

// DeepLinks builds best-effort import deep-links for the popular clients, most
// popular first. Schemes drift across client releases — verify periodically.
//
// happCrypt hands Happ the subscription as an encrypted happ://crypt4/ link, which
// it adds without showing the address. Should encrypting fail — it cannot for any
// address this panel builds — the button falls back to the plain link rather than
// disappearing: the page is how a user gets connected at all.
func DeepLinks(subURL string, lang i18n.Lang, happCrypt bool) []DeepLink {
	enc := url.QueryEscape(subURL)
	// Only the generic platform blurbs are translated; the OS names below are
	// proper nouns and read the same in every language.
	all := i18n.T(lang, "sub.allPlatforms")
	allTV := i18n.T(lang, "sub.allPlusTV")
	// Shadowrocket's sub:// URI carries the subscription URL base64-encoded (NOT
	// percent-encoded) — feeding it a %-escaped URL makes it fail with "invalid URL".
	subB64 := base64.StdEncoding.EncodeToString([]byte(subURL))
	happ := "happ://add/" + subURL
	if happCrypt {
		if l, err := extsub.EncryptHapp(subURL); err == nil {
			happ = l
		}
	}
	return []DeepLink{
		{"Happ", allTV, template.URL(happ)},
		{"INCY", allTV, template.URL("incy://import/" + subURL)},
		{"v2RayTun", allTV, template.URL("v2raytun://import/" + subURL)},
		{"Hiddify", all, template.URL("hiddify://import/" + subURL)},
		{"Karing", allTV, template.URL("karing://install-config?url=" + enc)},
		{"sing-box", all, template.URL("sing-box://import-remote-profile?url=" + enc)},
		{"Clash Meta / Mihomo", "Windows · macOS · Linux · Android", template.URL("clash://install-config?url=" + enc)},
		{"V2Box", "iOS · macOS · Android", template.URL("v2box://install-sub?url=" + enc)},
		{"v2rayNG", "Android", template.URL("v2rayng://install-sub?url=" + enc)},
		{"NekoBox", "Android", template.URL("sn://subscription?url=" + enc)},
		{"Streisand", "iOS · macOS · tvOS", template.URL("streisand://import/" + subURL)},
		{"Shadowrocket", "iOS · macOS · tvOS", template.URL("shadowrocket://add/sub://" + subB64)},
	}
}

// HappLink is the user's subscription as an encrypted happ://crypt4/ link, or ""
// when the operator has not switched encrypted Happ links on.
func HappLink(set *model.Settings, token string) (string, error) {
	if !set.SubHappCrypt {
		return "", nil
	}
	return extsub.EncryptHapp(URL(set, token))
}

// AWGConfURL is where a user downloads their AmneziaWG config for one server
// (0 = the master): <sub>/awg/<id>.conf; the QR of the same text is <id>.png.
func AWGConfURL(set *model.Settings, token string, serverID int64) string {
	return fmt.Sprintf("%s/awg/%d.conf", URL(set, token), serverID)
}

// AWGFileName is the config's file name — the Amnezia apps show it as the
// tunnel's name, so it is the server's label with everything a file system or a
// header would object to replaced.
func AWGFileName(set *model.Settings) string {
	// No user: this is a file name, not a connection name. A name carrying per-user
	// variables renders them as NameUnknown here, which the sanitiser below drops —
	// the right answer, since a downloaded file should not be stamped with somebody's
	// remaining quota at the moment they clicked.
	return confFileName(set.ProtoLabel(model.ProtoAWG), "amneziawg")
}

// TurnConfURL is where a user downloads their WireGuard config for one WireGuard inbound
// behind a TURN relay: <sub>/wg/<inbound id>.conf; the QR of the same text is <id>.png.
// Keyed by the inbound rather than the server, since one server may have several.
func TurnConfURL(set *model.Settings, token string, inboundID int64) string {
	return fmt.Sprintf("%s/wg/%d.conf", URL(set, token), inboundID)
}

// TurnFileName is a WireGuard inbound's config file name, from its label as AWGFileName
// makes one from the lane's.
func TurnFileName(in model.Inbound, set *model.Settings) string {
	return confFileName(link.CustomLabel(in, set), "wireguard")
}

// confFileName makes a tunnel's config file name from its label.
func confFileName(label, fallback string) string {
	var b strings.Builder
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ', r == '·', r == '.':
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = fallback
	}
	if len(name) > 15 { // wg interface names are 15 bytes; the apps derive one from the file
		name = name[:15]
	}
	return name + ".conf"
}

// TurnImportLink is the connection link the VK Turn Proxy app for iOS imports
// (anton48/vk-turn-proxy-ios): vkturnproxy://import?data=<base64url of a JSON
// {"version":1,"type":"connection","settings":{…}}>, tapped or pasted from the
// clipboard. It carries everything the app's server entry needs — the user's WireGuard
// key and tunnel address, the inbound's public key, the relay's address, the masking
// key and the call link — so nothing is typed by hand. "" when the user has no tunnel
// identity yet or the inbound no masking key.
//
// The mode is the app's SRTP-WRAP-S, its name for Free Turn Proxy's masked wire, which is
// what the panel's relay serves unshaped. The mode switches are all spelled out: the
// app applies them as a set, and one left out keeps whatever mode the device was in.
func TurnImportLink(u model.User, s *model.Settings, in model.Inbound) string {
	addr, ok := awg.ClientAddr(u.AWGSlot)
	if !ok || u.WGPrivateKey == "" || in.Opts.WGPublicKey == "" || in.Opts.TurnMaskKey == "" {
		return ""
	}
	settings := map[string]any{
		"serverName":    link.CustomLabel(in, s),
		"privateKey":    u.WGPrivateKey,
		"peerPublicKey": in.Opts.WGPublicKey,
		// Present though empty: the app's builds before 134 refuse a link without it,
		// and an empty key is WireGuard's "no preshared key".
		"presharedKey":  "",
		"tunnelAddress": addr.String() + "/32",
		"dnsServers":    awg.TunnelDNS(s),
		// Empty keeps the call link the app already has.
		"vkLink":      in.Opts.TurnLink,
		"peerAddress": net.JoinHostPort(s.Host, strconv.Itoa(in.Port)),
		"useDTLS":     true,
		"useSrtp":     false,
		"useWrap":     false,
		"useWrapA":    false,
		"useWrapS":    true,
		"wrapKeyHex":  in.Opts.TurnMaskKey,
		"obfProfile":  model.TurnMaskProfile,
		"useUDP":      false,
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "type": "connection", "settings": settings})
	if err != nil {
		return ""
	}
	return "vkturnproxy://import?data=" + base64.RawURLEncoding.EncodeToString(raw)
}

// FreeTurnImportLink is Free Turn Proxy's share link (samosvalishe/free-turn-proxy,
// docs/uri.md): freeturn://<base64url of a version-1 JSON>. Its Android app imports it
// whole — the WireGuard config it carries included — and so does VK Turn Proxy on iOS;
// the command-line client takes it in place of its flags. "" when the user has no tunnel
// identity yet or the inbound no masking key.
func FreeTurnImportLink(u model.User, s *model.Settings, in model.Inbound, conf string) string {
	if conf == "" || in.Opts.TurnMaskKey == "" {
		return ""
	}
	payload := struct {
		V        int    `json:"v"`
		Provider string `json:"provider"`
		Peer     string `json:"peer"`
		Obf      string `json:"obf"`
		Key      string `json:"key"`
		Name     string `json:"name,omitempty"`
		VK       string `json:"vk,omitempty"` // the Android app's field for the call link
		WG       string `json:"wg"`
	}{
		V:        1,
		Provider: "vk",
		Peer:     net.JoinHostPort(s.Host, strconv.Itoa(in.Port)),
		Obf:      model.TurnMaskProfile,
		Key:      in.Opts.TurnMaskKey,
		Name:     link.CustomLabel(in, s),
		VK:       in.Opts.TurnLink,
		WG:       conf,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return "freeturn://" + base64.RawURLEncoding.EncodeToString(raw)
}

// TurnClientConf is the WireGuard config of one user for one WireGuard inbound: the
// tunnel pointed at the TURN client on the same device, which carries it to the relay.
// It is the file the subscription serves and what the freeturn:// link carries. "" for
// a user with no tunnel identity yet.
func TurnClientConf(u model.User, s *model.Settings, in model.Inbound) string {
	addr, ok := awg.ClientAddr(u.AWGSlot)
	if !ok || u.WGPrivateKey == "" || in.Opts.WGPublicKey == "" {
		return ""
	}
	return awg.ClientConfig{
		PrivateKey:      u.WGPrivateKey,
		Address:         addr,
		DNS:             awg.TunnelDNS(s),
		MTU:             TurnMTU,
		ServerPublicKey: in.Opts.WGPublicKey,
		Endpoint:        model.TurnClientListen,
	}.Render()
}

// TurnMTU is the tunnel MTU on the client end of a TURN lane: vk-turn-proxy and Free
// Turn Proxy both ask for 1280, their DTLS, mask and TURN framing riding inside the
// path MTU as well.
const TurnMTU = 1280

// WingsVImportLink is the link WINGS V imports as a VK TURN profile — the Android app
// (WINGS-N/WINGSV) and its desktop build for Windows and Linux (WINGS V DeX) alike:
// wingsv://<base64url of 0x12 ‖ zlib(protobuf wingsv.Config)>. The Config is the shape
// the app exports for one saved VK TURN profile — the Turn settings plus the WireGuard
// sub-config they carry — so an import needs nothing typed. "" for a user with no tunnel
// identity yet.
//
// WINGS V does not speak Free Turn Proxy's mask, so it reaches the relay unmasked, which
// the relay still serves and the call service shapes. Two settings are pinned because
// the app's defaults assume its own server fork: the session mode is MAINLINE
// (vk-turn-proxy's own protocol; "auto" and "mu" negotiate a session layer the panel's
// relay does not have) and WRAP is OFF (unset means PREFERRED). The WireGuard endpoint is
// the app's local TURN client, as in the .conf.
func WingsVImportLink(u model.User, s *model.Settings, in model.Inbound) string {
	addr, ok := awg.ClientAddr(u.AWGSlot)
	priv, errPriv := base64.StdEncoding.DecodeString(u.WGPrivateKey)
	pub, errPub := base64.StdEncoding.DecodeString(in.Opts.WGPublicKey)
	if !ok || errPriv != nil || errPub != nil || len(priv) != 32 || len(pub) != 32 {
		return ""
	}
	// Field numbers are wingsv.proto's (app/src/main/proto/wingsv.proto).
	const (
		configTypeVKTurnProfile = 10
		backendVKTurn           = 7
		sessionModeMainline     = 2
		tunnelModeWireGuard     = 1
		wrapModeOff             = 1
	)
	title := link.CustomLabel(in, s)
	endpoint := func(host string, port int) []byte {
		return pbVarint(pbBytes(nil, 1, []byte(host)), 2, uint64(port))
	}
	localHost, localPortStr, _ := net.SplitHostPort(model.TurnClientListen)
	localPort, _ := strconv.Atoi(localPortStr)
	local := endpoint(localHost, localPort)

	turn := pbBytes(nil, 1, endpoint(s.Host, in.Port))
	if in.Opts.TurnLink != "" {
		turn = pbBytes(turn, 2, []byte(in.Opts.TurnLink))
	}
	turn = pbBytes(turn, 6, local)
	turn = pbVarint(turn, 9, sessionModeMainline)
	turn = pbVarint(turn, 18, tunnelModeWireGuard)
	turn = pbVarint(turn, 19, wrapModeOff)
	turn = pbBytes(turn, 23, []byte(title))

	iface := pbBytes(nil, 1, priv)
	iface = pbBytes(iface, 2, []byte(addr.String()+"/32"))
	for _, d := range strings.Split(awg.TunnelDNS(s), ",") {
		if d = strings.TrimSpace(d); d != "" {
			iface = pbBytes(iface, 3, []byte(d))
		}
	}
	iface = pbVarint(iface, 4, TurnMTU)
	wg := pbBytes(nil, 1, iface)
	wg = pbBytes(wg, 2, pbBytes(nil, 1, pub))
	wg = pbBytes(wg, 3, local)
	wg = pbBytes(wg, 4, []byte(title))

	config := pbVarint(nil, 1, 1)
	config = pbVarint(config, 2, configTypeVKTurnProfile)
	config = pbBytes(config, 3, turn)
	config = pbBytes(config, 4, wg)
	config = pbVarint(config, 5, backendVKTurn)

	var z bytes.Buffer
	z.WriteByte(0x12) // the app's frame byte for zlib-deflated protobuf
	zw, err := zlib.NewWriterLevel(&z, zlib.BestCompression)
	if err != nil {
		return ""
	}
	if _, err := zw.Write(config); err != nil || zw.Close() != nil {
		return ""
	}
	return "wingsv://" + base64.RawURLEncoding.EncodeToString(z.Bytes())
}

// pbBytes appends a length-delimited protobuf field (a string, bytes or a message).
func pbBytes(b []byte, field int, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

// pbVarint appends a varint protobuf field (an integer, enum or bool).
func pbVarint(b []byte, field int, v uint64) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3)
	return binary.AppendUvarint(b, v)
}
