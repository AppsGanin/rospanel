package proxypool

import "testing"

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
		if e.Protocol == "vless" && e.Link != link {
			t.Fatalf("the first spelling of a duplicated link must win: %s", e.Link)
		}
	}
}
