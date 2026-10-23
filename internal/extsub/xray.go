package extsub

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// XrayOutbound turns a share link into an Xray outbound object, the way an
// Xray-core app reads the same link. It serves two consumers with one reading of
// the link: the server, which dials an external endpoint as the upstream of an
// egress lane, and the subscription, which hands an app a ready-made config.
// Returns false for a scheme or a shape the format cannot carry.
func XrayOutbound(raw, tag string) (map[string]any, bool) {
	if strings.HasPrefix(strings.ToLower(raw), "vmess://") {
		return vmessOutbound(raw, tag)
	}
	l, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || l.User == nil {
		return nil, false
	}
	port, err := strconv.Atoi(l.Port())
	if err != nil || port <= 0 {
		return nil, false
	}
	host := l.Hostname()
	q := l.Query()
	cred := l.User.Username()
	out := map[string]any{"tag": tag}
	switch strings.ToLower(l.Scheme) {
	case "wireguard", "wg":
		return wireguardOutbound(l, tag, host, port)
	case "vless":
		enc := q.Get("encryption")
		if enc == "" {
			enc = "none"
		}
		if enc != "none" && !validVLESSEncryption(enc) {
			// A value Xray refuses fails the whole config it sits in — the server's
			// own, for a relayed or upstream server — so the link is not taken.
			return nil, false
		}
		user := map[string]any{"id": cred, "encryption": enc}
		if flow := q.Get("flow"); flow != "" {
			user["flow"] = flow
		}
		out["protocol"] = "vless"
		out["settings"] = map[string]any{"vnext": []map[string]any{
			{"address": host, "port": port, "users": []map[string]any{user}},
		}}
	case "trojan":
		out["protocol"] = "trojan"
		out["settings"] = map[string]any{"servers": []map[string]any{
			{"address": host, "port": port, "password": cred},
		}}
	case "hysteria2", "hysteria", "hy2":
		// The client wants version 2 in TWO places — the outbound settings and
		// streamSettings.hysteriaSettings — and the address/port in the settings
		// block rather than a servers/vnext list. Verified against Xray 26.7.28;
		// either version omitted and the client refuses to load.
		password, err := url.QueryUnescape(cred)
		if err != nil {
			password = cred
		}
		out["protocol"] = "hysteria"
		out["settings"] = map[string]any{"version": 2, "address": host, "port": port}
		out["streamSettings"] = hysteriaStream(q, password)
		return out, true
	case "ss":
		method, password, ok := ShadowsocksUserinfo(cred)
		if !ok {
			return nil, false
		}
		out["protocol"] = "shadowsocks"
		out["settings"] = map[string]any{"servers": []map[string]any{
			{"address": host, "port": port, "method": method, "password": password},
		}}
		// A plain ss:// link carries no transport parameters.
		out["streamSettings"] = map[string]any{"network": "tcp", "security": "none"}
		return out, true
	default:
		return nil, false
	}
	stream := xrayStream(q)
	if stream["security"] == "none" && (out["protocol"] == "trojan" || user0Enc(out) == "none") && !privateAddress(host) {
		// Xray refuses a whole config holding plaintext VLESS or Trojan to a public
		// address (since 26.9.9) — the server's own, for an upstream or a relay.
		return nil, false
	}
	out["streamSettings"] = stream
	return out, true
}

// user0Enc is the VLESS encryption of the outbound's one user, "" for another
// protocol.
func user0Enc(out map[string]any) string {
	settings, _ := out["settings"].(map[string]any)
	vnext, _ := settings["vnext"].([]map[string]any)
	if len(vnext) == 0 {
		return ""
	}
	users, _ := vnext[0]["users"].([]map[string]any)
	if len(users) == 0 {
		return ""
	}
	enc, _ := users[0]["encryption"].(string)
	return enc
}

// privateAddress reports whether Xray lets an unencrypted VLESS or Trojan outbound
// dial host. It is a subset of Xray's own private lists, so what passes here
// passes there.
func privateAddress(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
			netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
	}
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h != "" && !strings.Contains(h, ".") {
		return true // a dotless name
	}
	for _, zone := range []string{"localhost", "local", "lan", "internal", "localdomain", "home.arpa"} {
		if h == zone || strings.HasSuffix(h, "."+zone) {
			return true
		}
	}
	return false
}

// vmessOutbound reads the JSON a vmess:// link carries. The fields are the ones
// every app writes (the v2rayN "share format"); the transport ones map onto the
// same streamSettings as a URL-style link's query.
func vmessOutbound(raw, tag string) (map[string]any, bool) {
	cfg, ok := vmessConfig(raw)
	if !ok {
		return nil, false
	}
	port := anyInt(cfg.Port)
	if port < 1 {
		return nil, false
	}
	security := strings.TrimSpace(cfg.SCY)
	if security == "" {
		security = "auto"
	}
	q := url.Values{}
	q.Set("type", firstNonEmpty(cfg.Net, "tcp"))
	if strings.EqualFold(cfg.TLS, "tls") {
		q.Set("security", "tls")
	}
	q.Set("sni", firstNonEmpty(cfg.SNI, cfg.Host))
	q.Set("fp", cfg.FP)
	q.Set("alpn", cfg.ALPN)
	q.Set("host", cfg.Host)
	q.Set("path", cfg.Path)
	q.Set("headerType", cfg.Type)
	if strings.EqualFold(cfg.Net, "grpc") {
		q.Set("serviceName", cfg.Path)
		if cfg.Type == "multi" {
			q.Set("mode", "multi")
		}
	}
	return map[string]any{
		"tag":      tag,
		"protocol": "vmess",
		"settings": map[string]any{"vnext": []map[string]any{{
			"address": cfg.Add, "port": port,
			"users": []map[string]any{{"id": cfg.ID, "alterId": anyInt(cfg.AID), "security": security}},
		}}},
		"streamSettings": xrayStream(q),
	}, true
}

// ShadowsocksUserinfo decodes the base64 "method:password" of an ss:// link. The
// panel's own links carry "method:serverKey:userKey" (Shadowsocks 2022), where
// everything after the first colon is the password. A userinfo that is not
// base64 at all (some apps write it plain) is read as is.
func ShadowsocksUserinfo(userinfo string) (method, password string, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(userinfo)
	if err != nil {
		if b, err = base64.StdEncoding.DecodeString(userinfo); err != nil {
			if unescaped, uerr := url.QueryUnescape(userinfo); uerr == nil && strings.Contains(unescaped, ":") {
				b = []byte(unescaped)
			} else {
				return "", "", false
			}
		}
	}
	method, password, ok = strings.Cut(string(b), ":")
	return method, password, ok && method != "" && password != ""
}

// hysteriaStream is the streamSettings of a Hysteria2 outbound: the QUIC
// transport, TLS with ALPN h3 (the handshake fails without it), the per-user auth,
// and — when the lane hops ports — the quicParams block the share link already
// carries in its `fm` parameter.
//
// Port hopping and congestion live under finalmask.quicParams, not in
// hysteriaSettings: Xray moved them there and now only logs a warning for the old
// place. The panel's own links hold exactly that object in `fm`, double-escaped
// (see link.Hysteria2); a foreign link's `mport` range is turned into the same.
func hysteriaStream(q url.Values, password string) map[string]any {
	tls := map[string]any{"serverName": q.Get("sni"), "alpn": []string{"h3"}, "allowInsecure": false}
	if pin := q.Get("pcs"); pin != "" {
		tls["pinnedPeerCertSha256"] = pin
	}
	s := map[string]any{
		"network":          "hysteria",
		"security":         "tls",
		"tlsSettings":      tls,
		"hysteriaSettings": map[string]any{"version": 2, "auth": password},
	}
	if fm := q.Get("fm"); fm != "" {
		// url.Values already decoded once; the link escapes it twice.
		if once, err := url.QueryUnescape(fm); err == nil {
			fm = once
		}
		var mask map[string]any
		if json.Unmarshal([]byte(fm), &mask) == nil && len(mask) > 0 {
			s["finalmask"] = mask
		}
	} else if mport := q.Get("mport"); mport != "" {
		s["finalmask"] = map[string]any{"quicParams": map[string]any{"udpHop": map[string]any{"ports": mport}}}
	}
	return s
}

// ServerOutbound is XrayOutbound for the panel's own Xray: port hopping goes in the
// form the pinned release reads. XrayOutbound keeps the quicParams.udpHop form share
// links carry, because it also writes configs for client apps, whose cores may be
// older — and an older core refuses the udp mask outright, where a newer one only
// ignores the old form (no hopping, one port).
func ServerOutbound(link, tag string) (map[string]any, bool) {
	o, ok := XrayOutbound(link, tag)
	if !ok {
		return o, ok
	}
	if ss, _ := o["streamSettings"].(map[string]any); ss != nil {
		if mask, _ := ss["finalmask"].(map[string]any); mask != nil {
			ss["finalmask"] = hopToUDPMask(mask)
		}
	}
	return o, ok
}

// udpHopMask is port hopping as a UDP mask: Xray reads it since 26.9.9, and fixed
// it in 26.9.30. The
// older quicParams.udpHop form passes `xray -test` there and is then ignored —
// every packet goes to one port.
func udpHopMask(ports any, interval any) map[string]any {
	settings := map[string]any{"mode": "intervalRemote", "remotePorts": ports}
	if interval != nil {
		settings["interval"] = interval
	}
	return map[string]any{"type": "udphop", "settings": settings}
}

// hopToUDPMask moves quicParams.udpHop (what share links and client configs carry)
// into the udp mask our Xray reads; the rest of the mask is kept.
func hopToUDPMask(mask map[string]any) map[string]any {
	qp, _ := mask["quicParams"].(map[string]any)
	hop, _ := qp["udpHop"].(map[string]any)
	if hop == nil || hop["ports"] == nil || hop["ports"] == "" {
		return mask
	}
	udp, isList := mask["udp"].([]any)
	if mask["udp"] != nil && !isList {
		return mask // a shape we do not know is left to Xray as it came
	}
	delete(qp, "udpHop")
	if len(qp) == 0 {
		delete(mask, "quicParams")
	}
	for _, m := range udp {
		if mm, _ := m.(map[string]any); mm["type"] == "udphop" {
			return mask // the link already hops the new way
		}
	}
	// Last in the list: Xray reverses it and takes only a first mask that dials
	// itself, which the hop is; anywhere else the config is refused.
	mask["udp"] = append(udp, udpHopMask(hop["ports"], hop["interval"]))
	return mask
}

// xrayStream maps the link's transport and security parameters onto Xray's
// streamSettings, transport by transport — the same fields internal/link writes.
func xrayStream(q url.Values) map[string]any {
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	s := map[string]any{"network": network}
	switch q.Get("security") {
	case "tls":
		tls := map[string]any{"serverName": q.Get("sni"), "allowInsecure": false}
		if fp := q.Get("fp"); fp != "" {
			tls["fingerprint"] = fp
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tls["alpn"] = strings.Split(alpn, ",")
		}
		// A self-signed (IP) certificate is pinned by its SHA-256 rather than
		// waved through: Xray takes the leaf hash as lowercase hex, which is what the
		// link's pcs carries.
		if pin := q.Get("pcs"); pin != "" {
			tls["pinnedPeerCertSha256"] = pin
		}
		s["security"] = "tls"
		s["tlsSettings"] = tls
	case "reality":
		s["security"] = "reality"
		s["realitySettings"] = map[string]any{
			"serverName": q.Get("sni"), "fingerprint": q.Get("fp"),
			"publicKey": q.Get("pbk"), "shortId": q.Get("sid"), "spiderX": q.Get("spx"),
		}
	default:
		s["security"] = "none"
	}
	switch network {
	case "ws":
		s["wsSettings"] = map[string]any{"path": q.Get("path"), "host": q.Get("host")}
	case "httpupgrade":
		s["httpupgradeSettings"] = map[string]any{"path": q.Get("path"), "host": q.Get("host")}
	case "xhttp":
		x := map[string]any{"path": q.Get("path"), "host": q.Get("host")}
		if mode := q.Get("mode"); mode != "" {
			x["mode"] = mode
		}
		if extra := q.Get("extra"); extra != "" {
			var raw json.RawMessage
			if json.Unmarshal([]byte(extra), &raw) == nil {
				x["extra"] = raw
			}
		}
		s["xhttpSettings"] = x
	case "grpc":
		g := map[string]any{"serviceName": q.Get("serviceName"), "multiMode": q.Get("mode") == "multi"}
		if a := q.Get("authority"); a != "" {
			g["authority"] = a
		}
		s["grpcSettings"] = g
	case "tcp":
		if q.Get("headerType") == "http" {
			s["tcpSettings"] = map[string]any{"header": map[string]any{
				"type": "http",
				"request": map[string]any{
					"path":    splitList(q.Get("path")),
					"headers": map[string]any{"Host": splitList(q.Get("host"))},
				},
			}}
		}
	}
	return s
}

func splitList(v string) []string {
	if v == "" {
		return []string{}
	}
	return strings.Split(v, ",")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// vlessEncClient matches a client-side VLESS Encryption string as Xray reads it:
// mlkem768x25519plus.<native|xorpub|random>.<0rtt|1rtt>, then padding and key fields.
var vlessEncClient = regexp.MustCompile(`^mlkem768x25519plus\.(native|xorpub|random)\.(0rtt|1rtt)(\.[A-Za-z0-9_-]+)+$`)

func validVLESSEncryption(s string) bool { return vlessEncClient.MatchString(s) }
