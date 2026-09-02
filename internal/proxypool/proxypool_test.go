package proxypool

import (
	"strings"
	"testing"
)

// A lane's source may mix proxies and share links; each becomes an upstream of
// its own kind, duplicates fall out either way, and what is neither is dropped.
func TestParseMixesProxiesAndShareLinks(t *testing.T) {
	link := "vless://11111111-2222-3333-4444-555555555555@9.9.9.9:443?security=tls&sni=a.b#one"
	eps := Parse([]string{
		"# comment",
		"1.2.3.4:1080",
		"socks5://user:pass@1.2.3.4:1081",
		"http://5.6.7.8:3128",
		"socks4://1.2.3.4:1082", // no Xray outbound for socks4
		link,
		link + "&renamed=1", // the same server (same credential) counted once
		"hysteria2://pw@8.8.8.8:443?sni=h#two",
		"vless://@nohost:443",
		"garbage",
	})
	got := map[string]int{}
	for _, e := range eps {
		got[e.Protocol]++
		if e.Link != "" && (e.Address == "" || e.Port == 0) {
			t.Errorf("link upstream without address: %+v", e)
		}
	}
	if got["socks"] != 2 || got["http"] != 1 || got["vless"] != 1 || got["hysteria2"] != 1 || len(eps) != 5 {
		t.Fatalf("parsed %d upstreams: %v", len(eps), got)
	}
	for _, e := range eps {
		if e.Protocol == "vless" && e.Link != strings.TrimSuffix(link, "#one") {
			t.Fatalf("the first spelling of a duplicated link must win, without its label: %s", e.Link)
		}
	}
}

// A provider renaming a server (a new #label on the same link) must not read as a
// changed upstream: the label never reaches the outbound, and a changed upstream
// restarts Xray.
func TestParseIgnoresTheLabelOfAShareLink(t *testing.T) {
	a := Parse([]string{"vless://id@9.9.9.9:443?security=tls&sni=a.b#one"})
	b := Parse([]string{"vless://id@9.9.9.9:443?security=tls&sni=a.b#two"})
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("renamed server read as a different upstream: %+v vs %+v", a, b)
	}
}
