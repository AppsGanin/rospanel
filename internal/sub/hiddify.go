package sub

import (
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// hiddifyUA matches Hiddify ("HiddifyNext/5.5.5 (android) like ClashMeta v2ray sing-box").
var hiddifyUA = regexp.MustCompile(`(?i)^hiddifynext/v?(\d+)\.(\d+)\.(\d+)`)

// HiddifyLinkList reports whether a Hiddify client is served the link list rather than
// the Clash profile its User-Agent asks for. Hiddify reads a Clash profile into sing-box,
// and a proxy type it does not map fails the whole profile, so Clash carries it none of
// the newer lanes; its link parser (ray2sing) skips a line it cannot read and from core
// v5 reads naive+https://, anytls:// and mierus://. 5.5.5 is the first stable 5.x
// (hiddify-core 5.5.5); 4.x keeps the Clash profile.
func HiddifyLinkList(userAgent string) bool {
	m := hiddifyUA.FindStringSubmatch(userAgent)
	return m != nil && versionAtLeast(m[1:], 5, 5, 5)
}

// hiddifyLinks rewrites a link list for Hiddify's parser (ray2sing), which reads
// hysteria2:// in sing-box's terms: it unescapes fm= once where Xray's form is escaped
// twice, so the whole line fails and is dropped; it takes port hopping only as a port
// range in the address; and its hysteria2 TLS ignores the certificate pins (pcs,
// pinSHA256). So each hysteria2:// line loses fm, gets the hop range as its port and,
// when it pins the server's self-signed certificate, insecure=1 — what the Clash profile
// gave Hiddify as skip-cert-verify (a pin under a borrowed SNI is left out, dropPinnedFor). Obfuscation stays in obfs/obfs-password, which it reads.
func hiddifyLinks(links []string) []string {
	out := make([]string, len(links))
	for i, l := range links {
		out[i] = hiddifyHysteria2(l)
	}
	return out
}

func hiddifyHysteria2(l string) string {
	if !strings.HasPrefix(l, "hysteria2://") {
		return l
	}
	u, err := url.Parse(l)
	if err != nil {
		return l
	}
	q := u.Query()
	if fm := q.Get("fm"); fm != "" {
		q.Del("fm")
		if ports := fmHopPorts(fm); ports != "" {
			u.Host = net.JoinHostPort(u.Hostname(), ports)
		}
	}
	if q.Get("pcs") != "" || q.Get("pinSHA256") != "" {
		q.Set("insecure", "1")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// fmHopPorts is the hop range ("20000-20100") of a hysteria2 link's fm value as url.Values
// reads it (still escaped once: link.Hysteria2 escapes the document twice), or "".
func fmHopPorts(fm string) string {
	doc, err := url.QueryUnescape(fm)
	if err != nil {
		return ""
	}
	var parsed struct {
		QuicParams *struct {
			UDPHop *struct {
				Ports string `json:"ports"`
			} `json:"udpHop"`
		} `json:"quicParams"`
	}
	if json.Unmarshal([]byte(doc), &parsed) != nil || parsed.QuicParams == nil || parsed.QuicParams.UDPHop == nil {
		return ""
	}
	return parsed.QuicParams.UDPHop.Ports
}
