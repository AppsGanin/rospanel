package happ

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Decode takes the raw body of a subscription response and returns a list of
// proxy URI strings. It handles three input shapes in order:
//
//  1. happ://crypt* deep link → Decrypt → URI list
//  2. Base64-encoded URI list → decode → URI list
//  3. Plain-text URI list (fallback)
func Decode(body []byte) []string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return nil
	}

	// Shape 1: happ://crypt* deep link (the whole body is one link).
	if IsHappLink(s) {
		plain, err := Decrypt(s)
		if err != nil {
			return nil
		}
		return splitLines(string(plain))
	}

	// Shape 2: try base64 decode. A valid base64 blob decodes to printable text
	// whose first non-empty line looks like a URI scheme. We try multiple
	// encodings because subscriptions use both standard and URL-safe base64.
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := enc.DecodeString(strings.ReplaceAll(s, "\n", "")); err == nil {
			lines := splitLines(string(decoded))
			if looksLikeURIList(lines) {
				return lines
			}
		}
	}

	// Shape 3: treat as plain text.
	return splitLines(s)
}

// looksLikeURIList returns true when at least one line starts with a known
// proxy URI scheme. Used to confirm a base64-decoded payload is really a URI list.
func looksLikeURIList(lines []string) bool {
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if isKnownScheme(l) {
			return true
		}
	}
	return false
}

func isKnownScheme(s string) bool {
	for _, pfx := range []string{"vless://", "vmess://", "trojan://", "ss://", "hysteria2://", "hysteria://"} {
		if strings.HasPrefix(s, pfx) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, "\r")
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// ParseURIs parses a list of raw proxy URI strings into HappNode values.
// Lines that are not recognised proxy URIs are silently skipped.
// The subscriptionID is used to compute the identity key.
func ParseURIs(lines []string, subscriptionID int64) []Node {
	seen := make(map[string]struct{})
	var out []Node
	for _, raw := range lines {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		node, ok := parseURI(raw, subscriptionID)
		if !ok {
			continue
		}
		if _, dup := seen[node.IdentityKey]; dup {
			continue
		}
		seen[node.IdentityKey] = struct{}{}
		out = append(out, node)
	}
	return out
}

// parseURI attempts to parse a single proxy URI into a Node.
func parseURI(raw string, subscriptionID int64) (Node, bool) {
	switch {
	case strings.HasPrefix(raw, "vless://"):
		return parseVLESS(raw, subscriptionID)
	case strings.HasPrefix(raw, "vmess://"):
		return parseVMess(raw, subscriptionID)
	case strings.HasPrefix(raw, "trojan://"):
		return parseTrojan(raw, subscriptionID)
	case strings.HasPrefix(raw, "ss://"):
		return parseSS(raw, subscriptionID)
	case strings.HasPrefix(raw, "hysteria2://"), strings.HasPrefix(raw, "hysteria://"):
		return parseHysteria2(raw, subscriptionID)
	}
	return Node{}, false
}

// ── VLESS ─────────────────────────────────────────────────────────────────

func parseVLESS(raw string, subID int64) (Node, bool) {
	// vless://uuid@host:port?params#name
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return Node{}, false
	}
	host, portStr, ok := splitHostPort(u.Host)
	if !ok {
		return Node{}, false
	}
	uuid := u.User.Username()
	name := cleanName(u.Fragment)
	key := IdentityKeyFor(subID, "vless", host, portStr, uuid)
	return Node{
		SubscriptionID: subID,
		IdentityKey:    key,
		Name:           name,
		Protocol:       "vless",
		Host:           host,
		Port:           portStr,
		URI:            raw,
	}, true
}

// ── VMess ─────────────────────────────────────────────────────────────────

// vmessConfig is the JSON object inside a vmess:// base64 payload.
type vmessConfig struct {
	Add  string `json:"add"`
	Port any    `json:"port"` // may be string or int
	ID   string `json:"id"`
	PS   string `json:"ps"` // name/remark
}

func parseVMess(raw string, subID int64) (Node, bool) {
	// vmess://base64json
	b64 := strings.TrimPrefix(raw, "vmess://")
	b64 = strings.TrimRight(b64, "=")
	for len(b64)%4 != 0 {
		b64 += "="
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(b64)
		if err != nil {
			return Node{}, false
		}
	}
	var cfg vmessConfig
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.Add == "" {
		return Node{}, false
	}
	port := anyToInt(cfg.Port)
	if port == 0 {
		return Node{}, false
	}
	name := cleanName(cfg.PS)
	key := IdentityKeyFor(subID, "vmess", cfg.Add, port, cfg.ID)
	return Node{
		SubscriptionID: subID,
		IdentityKey:    key,
		Name:           name,
		Protocol:       "vmess",
		Host:           cfg.Add,
		Port:           port,
		URI:            raw,
	}, true
}

// ── Trojan ───────────────────────────────────────────────────────────────

func parseTrojan(raw string, subID int64) (Node, bool) {
	// trojan://password@host:port?params#name
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return Node{}, false
	}
	host, port, ok := splitHostPort(u.Host)
	if !ok {
		return Node{}, false
	}
	password := u.User.Username()
	name := cleanName(u.Fragment)
	key := IdentityKeyFor(subID, "trojan", host, port, password)
	return Node{
		SubscriptionID: subID,
		IdentityKey:    key,
		Name:           name,
		Protocol:       "trojan",
		Host:           host,
		Port:           port,
		URI:            raw,
	}, true
}

// ── Shadowsocks ──────────────────────────────────────────────────────────

func parseSS(raw string, subID int64) (Node, bool) {
	// ss://base64(method:password)@host:port#name  OR
	// ss://base64(method:password@host:port)#name  (legacy)
	u, err := url.Parse(raw)
	if err != nil {
		return Node{}, false
	}
	name := cleanName(u.Fragment)

	// Modern format: userinfo is base64(method:password)
	if u.User != nil && u.Host != "" {
		host, port, ok := splitHostPort(u.Host)
		if !ok {
			return Node{}, false
		}
		userinfo := u.User.Username()
		key := IdentityKeyFor(subID, "ss", host, port, userinfo)
		return Node{
			SubscriptionID: subID,
			IdentityKey:    key,
			Name:           name,
			Protocol:       "ss",
			Host:           host,
			Port:           port,
			URI:            raw,
		}, true
	}

	// Legacy format: entire authority is base64 encoded
	b64 := strings.TrimPrefix(raw, "ss://")
	if idx := strings.IndexByte(b64, '#'); idx >= 0 {
		b64 = b64[:idx]
	}
	b64 = strings.TrimRight(b64, "=")
	for len(b64)%4 != 0 {
		b64 += "="
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return Node{}, false
	}
	// decoded = "method:password@host:port"
	at := strings.LastIndex(string(decoded), "@")
	if at < 0 {
		return Node{}, false
	}
	authority := string(decoded[at+1:])
	host, port, ok := splitHostPort(authority)
	if !ok {
		return Node{}, false
	}
	userinfo := string(decoded[:at])
	key := IdentityKeyFor(subID, "ss", host, port, userinfo)
	return Node{
		SubscriptionID: subID,
		IdentityKey:    key,
		Name:           name,
		Protocol:       "ss",
		Host:           host,
		Port:           port,
		URI:            raw,
	}, true
}

// ── Hysteria2 ────────────────────────────────────────────────────────────

func parseHysteria2(raw string, subID int64) (Node, bool) {
	// hysteria2://password@host:port?params#name
	// hysteria://password@host:port?params#name (v1, treat same)
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return Node{}, false
	}
	host, port, ok := splitHostPort(u.Host)
	if !ok {
		return Node{}, false
	}
	password := u.User.Username()
	name := cleanName(u.Fragment)
	key := IdentityKeyFor(subID, "hysteria2", host, port, password)
	return Node{
		SubscriptionID: subID,
		IdentityKey:    key,
		Name:           name,
		Protocol:       "hysteria2",
		Host:           host,
		Port:           port,
		URI:            raw,
	}, true
}

// ── helpers ──────────────────────────────────────────────────────────────

// splitHostPort splits "host:port" → (host, port, ok).
func splitHostPort(hostport string) (host string, port int, ok bool) {
	// Handle IPv6 [::1]:port
	if strings.HasPrefix(hostport, "[") {
		end := strings.LastIndex(hostport, "]")
		if end < 0 {
			return
		}
		host = hostport[1:end]
		rest := hostport[end+1:]
		if len(rest) == 0 {
			// No port — use 0 but still ok if host is non-empty
			return host, 0, host != ""
		}
		if !strings.HasPrefix(rest, ":") {
			return
		}
		portStr := rest[1:]
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return
		}
		return host, p, true
	}
	colon := strings.LastIndex(hostport, ":")
	if colon < 0 {
		return hostport, 0, hostport != ""
	}
	host = hostport[:colon]
	portStr := hostport[colon+1:]
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return
	}
	return host, p, true
}

func cleanName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Unnamed"
	}
	return s
}

func anyToInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	case int:
		return n
	}
	return 0
}
