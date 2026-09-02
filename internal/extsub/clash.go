package extsub

import (
	"fmt"
	"net/url"
	"strings"
)

// The subscription formats that are not a list of links need each external server
// rewritten in their own vocabulary. Both converters here follow the rule the
// panel's own lanes follow (model.SupportsClash / SupportsSingBox): a combination
// the client cannot express is DROPPED rather than approximated, because an app
// that rejects one entry usually rejects the whole profile.

// ClashProxy renders an endpoint as one Clash Meta (mihomo) proxy line, flow-style
// YAML, with the name the entry goes by. sv is the profile's skip-cert-verify
// value; it is never raised for a foreign server (a link carries no proof its
// certificate is self-signed, and "trust anything" is the wrong default).
func ClashProxy(ep Endpoint) (name, line string, ok bool) {
	name = ep.Name
	switch ep.Protocol {
	case "vmess":
		return name, clashVMess(ep, name), true
	case "ss":
		l, err := url.Parse(ep.Link)
		if err != nil || l.User == nil {
			return "", "", false
		}
		method, password, ok := ShadowsocksUserinfo(l.User.Username())
		if !ok {
			return "", "", false
		}
		return name, fmt.Sprintf("  - {name: %q, type: ss, server: %q, port: %d, cipher: %q, password: %q, udp: true}",
			name, ep.Host, ep.Port, method, password), true
	}
	l, err := url.Parse(ep.Link)
	if err != nil || l.User == nil {
		return "", "", false
	}
	q := l.Query()
	cred := l.User.Username()
	switch ep.Protocol {
	case "hysteria2":
		password, err := url.QueryUnescape(cred)
		if err != nil {
			password = cred
		}
		ports := ""
		if mport := q.Get("mport"); mport != "" {
			ports = fmt.Sprintf(", ports: %q", mport)
		}
		return name, fmt.Sprintf("  - {name: %q, type: hysteria2, server: %q, port: %d, password: %q, sni: %q, alpn: [h3], skip-cert-verify: false%s}",
			name, ep.Host, ep.Port, password, q.Get("sni"), ports), true
	case "vless", "trojan":
		network := firstNonEmpty(q.Get("type"), "tcp")
		transport, ok := clashTransport(network, q)
		if !ok {
			return "", "", false
		}
		security := q.Get("security")
		if ep.Protocol == "trojan" && security != "tls" {
			return "", "", false // mihomo's trojan is TLS by definition
		}
		var b strings.Builder
		fmt.Fprintf(&b, "  - {name: %q, type: %s, server: %q, port: %d, ", name, ep.Protocol, ep.Host, ep.Port)
		if ep.Protocol == "vless" {
			fmt.Fprintf(&b, "uuid: %q, ", cred)
			if flow := q.Get("flow"); flow != "" {
				fmt.Fprintf(&b, "flow: %s, ", flow)
			}
		} else {
			fmt.Fprintf(&b, "password: %q, ", cred)
		}
		fmt.Fprintf(&b, "network: %s, udp: true", network)
		switch security {
		case "tls":
			fmt.Fprintf(&b, ", tls: true, servername: %q, skip-cert-verify: false", q.Get("sni"))
			if fp := q.Get("fp"); fp != "" {
				fmt.Fprintf(&b, ", client-fingerprint: %s", fp)
			}
			if alpn := q.Get("alpn"); alpn != "" {
				fmt.Fprintf(&b, ", alpn: [%s]", alpn)
			}
		case "reality":
			if ep.Protocol != "vless" {
				return "", "", false
			}
			fmt.Fprintf(&b, ", tls: true, servername: %q, client-fingerprint: %s, reality-opts: {public-key: %q, short-id: %q}",
				q.Get("sni"), firstNonEmpty(q.Get("fp"), "chrome"), q.Get("pbk"), q.Get("sid"))
		}
		b.WriteString(transport)
		b.WriteString("}")
		return name, b.String(), true
	}
	return "", "", false
}

// clashTransport is the per-transport options fragment; false for a transport
// mihomo has no words for (XHTTP above all).
func clashTransport(network string, q url.Values) (string, bool) {
	switch network {
	case "tcp":
		if q.Get("headerType") == "http" {
			return "", false // the HTTP-header masquerade has no mihomo form
		}
		return "", true
	case "ws":
		return fmt.Sprintf(", ws-opts: {path: %q, headers: {Host: %q}}", firstNonEmpty(q.Get("path"), "/"), q.Get("host")), true
	case "httpupgrade":
		return fmt.Sprintf(", network: ws, ws-opts: {path: %q, headers: {Host: %q}, v2ray-http-upgrade: true}",
			firstNonEmpty(q.Get("path"), "/"), q.Get("host")), true
	case "grpc":
		return fmt.Sprintf(", grpc-opts: {grpc-service-name: %q}", q.Get("serviceName")), true
	}
	return "", false
}

func clashVMess(ep Endpoint, name string) string {
	cfg, _ := vmessConfig(ep.Link)
	var b strings.Builder
	fmt.Fprintf(&b, "  - {name: %q, type: vmess, server: %q, port: %d, uuid: %q, alterId: %d, cipher: %s, udp: true",
		name, ep.Host, ep.Port, cfg.ID, anyInt(cfg.AID), firstNonEmpty(cfg.SCY, "auto"))
	network := firstNonEmpty(cfg.Net, "tcp")
	q := url.Values{}
	q.Set("path", cfg.Path)
	q.Set("host", cfg.Host)
	q.Set("serviceName", cfg.Path)
	q.Set("headerType", cfg.Type)
	if transport, ok := clashTransport(network, q); ok {
		fmt.Fprintf(&b, ", network: %s%s", network, transport)
	}
	if strings.EqualFold(cfg.TLS, "tls") {
		fmt.Fprintf(&b, ", tls: true, servername: %q, skip-cert-verify: false", firstNonEmpty(cfg.SNI, cfg.Host))
		if cfg.FP != "" {
			fmt.Fprintf(&b, ", client-fingerprint: %s", cfg.FP)
		}
	}
	b.WriteString("}")
	return b.String()
}
