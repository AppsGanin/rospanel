package model

import "testing"

func TestValidServerHost(t *testing.T) {
	for h, want := range map[string]bool{
		"vpn.example.com": true, "144.31.159.81": true, "2001:db8::1": true, "xn--80ak6aa92e.com": true,
		"A-B.example.COM": true, "vpn.example.com.": true, "vpn": true, "_srv.example.com": true,
		"": false, ".": false, "a.b..": false, "-a.example.com": false, "a..example.com": false,
		"example.com:443": false, "exa mple.com": false, "example.com\n[Interface]\nPreUp = id": false,
		"203.0.113.5\nPreUp = curl x | sh #": false, "пример.рф": false, "a.b/c": false,
	} {
		if got := ValidServerHost(h); got != want {
			t.Errorf("%q: %v, want %v", h, got, want)
		}
	}
}
