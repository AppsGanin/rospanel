package sub

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The Xray JSON subscription format: one full client config per lane per server,
// as a JSON array. It is what Xray-core apps (Happ, v2rayNG, v2rayN, Streisand)
// import when a subscription hands them JSON instead of links — and the only form
// in which client-side DPI evasion (fragment, noise) reaches them, since a share
// link has no field for either.
//
// Each config is derived from the share link the panel already builds for that
// lane, so the two can never disagree about a port, an SNI or a REALITY key: the
// link is parsed back into an outbound rather than assembled a second time.

// XrayJSONMulti renders the user's lanes across every server as an array of Xray
// configs. dpi decides whether a fragment/noise outbound is chained in.
func XrayJSONMulti(u model.User, servers []Server, dpi model.SubDPI) string {
	configs := make([]map[string]any, 0, 8)
	for _, l := range ShareLinksAll(u, servers) {
		if cfg, ok := xrayConfigFromLink(l, dpi); ok {
			configs = append(configs, cfg)
		}
	}
	b, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(b)
}

// xrayConfigFromLink turns one share link into a complete client config: local
// SOCKS/HTTP inbounds on the ports every Xray app expects, the proxy outbound, the
// optional fragment/noise dialer, direct and block, and a routing block that keeps
// private ranges local. Returns false for a scheme the format cannot carry.
func xrayConfigFromLink(raw string, dpi model.SubDPI) (map[string]any, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	proxy, ok := xrayOutbound(parsed)
	if !ok {
		return nil, false
	}
	outbounds := []map[string]any{proxy}
	// Fragment only where there is a ClientHello with our real SNI to hide — a
	// TLS-over-TCP lane. REALITY shows the donor's name and gains nothing from a
	// split; plain transports have no handshake. Noise has no such restriction.
	//
	// A QUIC lane is chained through neither: it never dials through the TCP
	// `freedom` outbound a dialerProxy points at, so one would do nothing at best.
	security := parsed.Query().Get("security")
	if quic := parsed.Scheme == "hysteria2"; !quic {
		if shaper := fragmentOutbound(dpi, security == "tls"); shaper != nil {
			stream, _ := proxy["streamSettings"].(map[string]any)
			stream["sockopt"] = map[string]any{"dialerProxy": "fragment", "tcpNoDelay": true}
			outbounds = append(outbounds, shaper)
		}
	}
	outbounds = append(outbounds,
		map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "UseIP"}},
		map[string]any{"tag": "block", "protocol": "blackhole", "settings": map[string]any{}},
	)
	remarks := parsed.Fragment
	if unescaped, err := url.PathUnescape(remarks); err == nil {
		remarks = unescaped
	}
	return map[string]any{
		"remarks": remarks,
		"log":     map[string]any{"loglevel": "warning"},
		"dns":     map[string]any{"servers": []any{"1.1.1.1", "8.8.8.8"}},
		"inbounds": []map[string]any{
			{
				"tag": "socks", "listen": "127.0.0.1", "port": 10808, "protocol": "socks",
				"settings": map[string]any{"auth": "noauth", "udp": true},
				"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": false},
			},
			{
				"tag": "http", "listen": "127.0.0.1", "port": 10809, "protocol": "http",
				"settings": map[string]any{"auth": "noauth"},
			},
		},
		"outbounds": outbounds,
		"routing": map[string]any{
			"domainStrategy": "IPIfNonMatch",
			// The private ranges are spelled out rather than written as
			// "geoip:private": that shorthand needs geoip.dat next to the client, and
			// a client without it refuses the WHOLE config rather than just the rule.
			// The literal list means the same thing and depends on nothing.
			"rules": []map[string]any{
				{"type": "field", "outboundTag": "direct", "ip": privateRanges()},
			},
		},
	}, true
}

// privateRanges is what "geoip:private" expands to: the loopback, link-local and
// RFC1918 blocks plus their IPv6 counterparts, kept local instead of tunnelled.
func privateRanges() []string {
	return []string{
		"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"169.254.0.0/16", "100.64.0.0/10", "224.0.0.0/4", "255.255.255.255/32",
		"::1/128", "fc00::/7", "fe80::/10",
	}
}

// fragmentOutbound is the freedom outbound the proxy dials through when fragment
// or noise is on. nil when neither applies to this lane. Both settings ride on one
// outbound: Xray applies fragment to the TLS handshake it sees and noise before
// the packets it sends, independently.
func fragmentOutbound(dpi model.SubDPI, tlsLane bool) map[string]any {
	dpi = dpi.Normalized()
	settings := map[string]any{}
	if dpi.Fragment && tlsLane {
		settings["fragment"] = map[string]any{
			"packets": dpi.FragmentPackets, "length": dpi.FragmentLength, "interval": dpi.FragmentInterval,
		}
	}
	if dpi.Noise {
		settings["noises"] = []map[string]any{{
			"type": dpi.NoiseType, "packet": dpi.NoisePacket, "delay": dpi.NoiseDelay,
		}}
	}
	if len(settings) == 0 {
		return nil
	}
	return map[string]any{
		"tag": "fragment", "protocol": "freedom", "settings": settings,
		"streamSettings": map[string]any{"sockopt": map[string]any{"tcpNoDelay": true}},
	}
}

// xrayOutbound builds the proxy outbound for a vless://, trojan:// or ss:// link.
func xrayOutbound(l *url.URL) (map[string]any, bool) {
	port, err := strconv.Atoi(l.Port())
	if err != nil || port <= 0 {
		return nil, false
	}
	host := l.Hostname()
	q := l.Query()
	cred := l.User.Username()
	out := map[string]any{"tag": "proxy"}
	switch l.Scheme {
	case "vless":
		user := map[string]any{"id": cred, "encryption": "none"}
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
	case "hysteria2":
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
		method, password, ok := shadowsocksUserinfo(cred)
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
	out["streamSettings"] = xrayStream(q)
	return out, true
}

// shadowsocksUserinfo decodes the base64 "method:password" of an ss:// link. The
// panel's own links carry "method:serverKey:userKey" (Shadowsocks 2022), where
// everything after the first colon is the password.
func shadowsocksUserinfo(userinfo string) (method, password string, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(userinfo)
	if err != nil {
		if b, err = base64.StdEncoding.DecodeString(userinfo); err != nil {
			return "", "", false
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
// place. `fm` holds exactly that object, double-escaped (see link.Hysteria2), so
// it is decoded and used as is rather than rebuilt — one source for both forms.
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
	}
	return s
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
