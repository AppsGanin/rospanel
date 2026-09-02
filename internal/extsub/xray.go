package extsub

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
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
	out["streamSettings"] = xrayStream(q)
	return out, true
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
