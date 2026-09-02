// Package extsub reads other people's subscriptions: a URL or a pasted payload
// that decodes to a list of share links (vless://, trojan://, ss://, vmess://,
// hysteria2://), in the plain, base64 or happ://crypt… form the apps exchange.
//
// What comes out is a list of Endpoint — the link plus the few facts the panel
// needs to name, deduplicate and route it. The panel uses those in two places: as
// the upstreams of an egress lane (traffic leaves the server through them) and as
// extra servers handed to users in their own subscriptions. Converting a link into
// the shape each consumer wants — an Xray outbound, a Clash proxy, a sing-box
// outbound — is the other half of this package.
package extsub

import (
	"bufio"
	"encoding/base64"
	"strings"
)

// Decode turns a subscription body into its share links. Three shapes are
// recognised, tried in the order they can be told apart: a happ://crypt… link
// (the whole body is one link), a base64 payload that decodes to links, and the
// links themselves. Blank lines and # comments are dropped either way.
func Decode(body []byte) []string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return nil
	}
	if IsHappLink(s) {
		plain, err := DecryptHapp(s)
		if err != nil {
			return nil
		}
		return Lines(string(plain))
	}
	// Base64 is only accepted when what it decodes to is a link list: a plain body
	// that happens to be valid base64 (a bare "vless" would be) must stay plain.
	compact := strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := enc.DecodeString(compact); err == nil {
			if lines := Lines(string(decoded)); hasShareLink(lines) {
				return lines
			}
		}
	}
	return Lines(s)
}

// Lines splits text into trimmed, non-empty, non-comment lines.
func Lines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// hasShareLink reports whether any line carries a scheme this package parses.
func hasShareLink(lines []string) bool {
	for _, l := range lines {
		if scheme, _, ok := strings.Cut(l, "://"); ok && knownScheme(strings.ToLower(scheme)) {
			return true
		}
	}
	return false
}

func knownScheme(s string) bool {
	switch s {
	case "vless", "vmess", "trojan", "ss", "hysteria2", "hysteria", "hy2":
		return true
	}
	return false
}
