package extsub

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Endpoint is one server from a subscription: the link as it will be handed on,
// and what the panel needs to know about it without parsing the link again.
type Endpoint struct {
	Protocol string // vless | vmess | trojan | ss | hysteria2
	Host     string
	Port     int
	Name     string // the link's label (#fragment / vmess "ps"), or a made-up one
	Link     string // the share link, exactly as received
}

// Key identifies the endpoint across syncs: the same server with the same
// credential keeps its key however its label or transport parameters change, so
// a re-sync updates a row rather than replacing it (and the operator's on/off
// choice for it survives).
func (e Endpoint) Key() string {
	sum := sha256.Sum256([]byte(e.Protocol + "\x00" + e.Host + "\x00" + strconv.Itoa(e.Port) + "\x00" + e.credential()))
	return hex.EncodeToString(sum[:16])
}

// credential is the part of the link that authenticates: the UUID, password or
// Shadowsocks userinfo.
func (e Endpoint) credential() string {
	if e.Protocol == "vmess" {
		if cfg, ok := vmessConfig(e.Link); ok {
			return cfg.ID
		}
		return ""
	}
	if u, err := url.Parse(e.Link); err == nil && u.User != nil {
		return u.User.Username()
	}
	return ""
}

// Parse reads one share link. Anything that is not a link this package
// understands — or is one with no usable host and port — reports false.
func Parse(raw string) (Endpoint, bool) {
	raw = strings.TrimSpace(raw)
	scheme, _, ok := strings.Cut(raw, "://")
	if !ok {
		return Endpoint{}, false
	}
	switch strings.ToLower(scheme) {
	case "vmess":
		return parseVMess(raw)
	case "ss":
		return parseShadowsocks(raw)
	case "vless", "trojan", "hysteria2", "hysteria", "hy2":
		return parseURLLink(raw)
	}
	return Endpoint{}, false
}

// ParseAll reads a list of links, keeping the first of any duplicates.
func ParseAll(lines []string) []Endpoint {
	seen := map[string]bool{}
	var out []Endpoint
	for _, l := range lines {
		ep, ok := Parse(l)
		if !ok {
			continue
		}
		if k := ep.Key(); !seen[k] {
			seen[k] = true
			out = append(out, ep)
		}
	}
	return out
}

// parseURLLink handles the schemes shaped like a URL: scheme://cred@host:port?…#name.
func parseURLLink(raw string) (Endpoint, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil || u.User.Username() == "" {
		return Endpoint{}, false
	}
	host, port, ok := hostPort(u.Host)
	if !ok {
		return Endpoint{}, false
	}
	proto := strings.ToLower(u.Scheme)
	if proto == "hysteria" || proto == "hy2" {
		proto = "hysteria2"
	}
	return Endpoint{
		Protocol: proto, Host: host, Port: port,
		Name: labelOr(u.Fragment, proto, host, port), Link: raw,
	}, true
}

// parseShadowsocks accepts both spellings: ss://base64(method:pass)@host:port#name
// and the older ss://base64(method:pass@host:port)#name. The link is kept as
// received either way; clients read both.
func parseShadowsocks(raw string) (Endpoint, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, false
	}
	if u.User != nil && u.Host != "" {
		host, port, ok := hostPort(u.Host)
		if !ok {
			return Endpoint{}, false
		}
		return Endpoint{Protocol: "ss", Host: host, Port: port, Name: labelOr(u.Fragment, "ss", host, port), Link: raw}, true
	}
	body := strings.TrimPrefix(raw, "ss://")
	if i := strings.IndexByte(body, '#'); i >= 0 {
		body = body[:i]
	}
	decoded, err := happBase64(body)
	if err != nil {
		return Endpoint{}, false
	}
	userinfo, authority, ok := strings.Cut(string(decoded), "@")
	if !ok {
		return Endpoint{}, false
	}
	host, port, ok := hostPort(authority)
	if !ok {
		return Endpoint{}, false
	}
	// Re-spell as the modern form so every consumer sees one shape, and so the
	// credential (the userinfo) is where Key looks for it.
	link := "ss://" + base64.RawURLEncoding.EncodeToString([]byte(userinfo)) + "@" + net.JoinHostPort(host, strconv.Itoa(port))
	if u.Fragment != "" {
		link += "#" + url.PathEscape(u.Fragment)
	}
	return Endpoint{Protocol: "ss", Host: host, Port: port, Name: labelOr(u.Fragment, "ss", host, port), Link: link}, true
}

// vmessLink is the JSON object a vmess:// link carries, base64-encoded.
type vmessLink struct {
	Add  string `json:"add"`
	Port any    `json:"port"` // a number or a string, depending on the app
	ID   string `json:"id"`
	AID  any    `json:"aid"`
	Net  string `json:"net"`
	Type string `json:"type"`
	Host string `json:"host"`
	Path string `json:"path"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
	FP   string `json:"fp"`
	ALPN string `json:"alpn"`
	SCY  string `json:"scy"`
	PS   string `json:"ps"`
}

func vmessConfig(raw string) (vmessLink, bool) {
	body := strings.TrimPrefix(strings.TrimSpace(raw), "vmess://")
	if i := strings.IndexByte(body, '#'); i >= 0 {
		body = body[:i]
	}
	data, err := happBase64(body)
	if err != nil {
		return vmessLink{}, false
	}
	var cfg vmessLink
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.Add == "" || cfg.ID == "" {
		return vmessLink{}, false
	}
	return cfg, true
}

func parseVMess(raw string) (Endpoint, bool) {
	cfg, ok := vmessConfig(raw)
	if !ok {
		return Endpoint{}, false
	}
	port := anyInt(cfg.Port)
	if port < 1 || port > 65535 {
		return Endpoint{}, false
	}
	return Endpoint{Protocol: "vmess", Host: cfg.Add, Port: port, Name: labelOr(cfg.PS, "vmess", cfg.Add, port), Link: raw}, true
}

// hostPort splits "host:port" (IPv6 in brackets) and checks the port.
func hostPort(hp string) (string, int, bool) {
	host, portStr, err := net.SplitHostPort(hp)
	if err != nil || host == "" {
		return "", 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return host, port, true
}

// labelOr is the link's own label, unescaped, or "PROTO host:port" when it has none.
func labelOr(fragment, proto, host string, port int) string {
	if s, err := url.PathUnescape(fragment); err == nil {
		fragment = s
	}
	if fragment = strings.TrimSpace(fragment); fragment != "" {
		return fragment
	}
	return fmt.Sprintf("%s %s", strings.ToUpper(proto), net.JoinHostPort(host, strconv.Itoa(port)))
}

func anyInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	case int:
		return n
	}
	return 0
}
