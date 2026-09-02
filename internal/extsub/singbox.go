package extsub

import (
	"net/url"
	"strings"
)

// SingBoxOutbound renders an endpoint as a sing-box outbound object with the given
// tag. False for what sing-box cannot express: XHTTP, the HTTP-header masquerade,
// a Trojan without TLS.
func SingBoxOutbound(ep Endpoint, tag string) (map[string]any, bool) {
	if ep.Protocol == "vmess" {
		return singboxVMess(ep, tag)
	}
	l, err := url.Parse(ep.Link)
	if err != nil || l.User == nil {
		return nil, false
	}
	q := l.Query()
	cred := l.User.Username()
	out := map[string]any{"tag": tag, "server": ep.Host, "server_port": ep.Port}
	switch ep.Protocol {
	case "ss":
		method, password, ok := ShadowsocksUserinfo(cred)
		if !ok {
			return nil, false
		}
		out["type"] = "shadowsocks"
		out["method"] = method
		out["password"] = password
		return out, true
	case "hysteria2":
		password, err := url.QueryUnescape(cred)
		if err != nil {
			password = cred
		}
		out["type"] = "hysteria2"
		out["password"] = password
		out["tls"] = map[string]any{"enabled": true, "server_name": q.Get("sni"), "alpn": []string{"h3"}, "insecure": false}
		if mport := q.Get("mport"); mport != "" {
			out["server_ports"] = []string{strings.ReplaceAll(mport, "-", ":")}
			out["hop_interval"] = "10s"
			delete(out, "server_port")
		}
		return out, true
	case "vless", "trojan":
		network := firstNonEmpty(q.Get("type"), "tcp")
		transport, ok := singboxTransport(network, q)
		if !ok {
			return nil, false
		}
		security := q.Get("security")
		if ep.Protocol == "trojan" && security != "tls" {
			return nil, false
		}
		out["type"] = ep.Protocol
		if ep.Protocol == "vless" {
			out["uuid"] = cred
			if flow := q.Get("flow"); flow != "" {
				out["flow"] = flow
			}
		} else {
			out["password"] = cred
		}
		switch security {
		case "tls":
			tls := map[string]any{"enabled": true, "server_name": q.Get("sni"), "insecure": false}
			if fp := q.Get("fp"); fp != "" {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
			}
			if alpn := q.Get("alpn"); alpn != "" {
				tls["alpn"] = strings.Split(alpn, ",")
			}
			out["tls"] = tls
		case "reality":
			if ep.Protocol != "vless" {
				return nil, false
			}
			out["tls"] = map[string]any{
				"enabled": true, "server_name": q.Get("sni"),
				"utls":    map[string]any{"enabled": true, "fingerprint": firstNonEmpty(q.Get("fp"), "chrome")},
				"reality": map[string]any{"enabled": true, "public_key": q.Get("pbk"), "short_id": q.Get("sid")},
			}
		}
		if transport != nil {
			out["transport"] = transport
		}
		return out, true
	}
	return nil, false
}

// singboxTransport is the transport block, nil for plain TCP, false for what
// sing-box has no transport for.
func singboxTransport(network string, q url.Values) (map[string]any, bool) {
	switch network {
	case "tcp":
		if q.Get("headerType") == "http" {
			return nil, false
		}
		return nil, true
	case "ws":
		t := map[string]any{"type": "ws", "path": firstNonEmpty(q.Get("path"), "/")}
		if host := q.Get("host"); host != "" {
			t["headers"] = map[string]any{"Host": host}
		}
		return t, true
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade", "path": firstNonEmpty(q.Get("path"), "/")}
		if host := q.Get("host"); host != "" {
			t["host"] = host
		}
		return t, true
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": q.Get("serviceName")}, true
	}
	return nil, false
}

func singboxVMess(ep Endpoint, tag string) (map[string]any, bool) {
	cfg, ok := vmessConfig(ep.Link)
	if !ok {
		return nil, false
	}
	q := url.Values{}
	q.Set("path", cfg.Path)
	q.Set("host", cfg.Host)
	q.Set("serviceName", cfg.Path)
	q.Set("headerType", cfg.Type)
	transport, ok := singboxTransport(firstNonEmpty(cfg.Net, "tcp"), q)
	if !ok {
		return nil, false
	}
	out := map[string]any{
		"tag": tag, "type": "vmess", "server": ep.Host, "server_port": ep.Port,
		"uuid": cfg.ID, "alter_id": anyInt(cfg.AID), "security": firstNonEmpty(cfg.SCY, "auto"),
	}
	if strings.EqualFold(cfg.TLS, "tls") {
		tls := map[string]any{"enabled": true, "server_name": firstNonEmpty(cfg.SNI, cfg.Host), "insecure": false}
		if cfg.FP != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": cfg.FP}
		}
		out["tls"] = tls
	}
	if transport != nil {
		out["transport"] = transport
	}
	return out, true
}
