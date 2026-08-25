package proxypool

import (
	"testing"
)

func TestParse(t *testing.T) {
	lines := []string{
		"# Comment line",
		"",
		"   ",
		"192.168.1.1:1080", // bare host:port -> socks
		"socks5://admin:secret@vpn.example.com:10800", // socks5 with auth
		"http://proxy.example.com:8080",               // http
		"socks4://unsupported.example.com:1080",       // unsupported -> skipped
		"invalid://bad_url",                           // invalid scheme
		"http://proxy.example.com:8080",               // duplicate -> skipped
		"socks5://valid.example.com:99999",            // invalid port (>65535) -> skipped
		"socks5://valid.example.com:0",                // invalid port (0) -> skipped
	}

	endpoints := Parse(lines)
	if len(endpoints) != 3 {
		t.Fatalf("Parse returned %d endpoints; want 3: %+v", len(endpoints), endpoints)
	}

	// 1. Bare socks
	if endpoints[0].Protocol != "socks" || endpoints[0].Address != "192.168.1.1" || endpoints[0].Port != 1080 {
		t.Errorf("endpoints[0] = %+v; want socks://192.168.1.1:1080", endpoints[0])
	}

	// 2. Socks5 with auth
	if endpoints[1].Protocol != "socks" || endpoints[1].Address != "vpn.example.com" || endpoints[1].Port != 10800 ||
		endpoints[1].User != "admin" || endpoints[1].Pass != "secret" {
		t.Errorf("endpoints[1] = %+v; want socks://admin:secret@vpn.example.com:10800", endpoints[1])
	}

	// 3. HTTP
	if endpoints[2].Protocol != "http" || endpoints[2].Address != "proxy.example.com" || endpoints[2].Port != 8080 {
		t.Errorf("endpoints[2] = %+v; want http://proxy.example.com:8080", endpoints[2])
	}
}
