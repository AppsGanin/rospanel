package model

import "testing"

// A page address must be absolute http(s), and must not send the browser back to a
// subscription link of the same panel, which would redirect it again forever.
func TestSubPageURL(t *testing.T) {
	t.Parallel()
	for raw, ok := range map[string]bool{
		"https://example.com/c?sub={token}": true,
		"http://example.com/{token}":        true,
		"/cabinet":                          false,
		"javascript:alert(1)":               false,
		"https://user:pw@example.com/":      false,
		"https://a b.example/":              false,
	} {
		if ValidSubPageURL(raw) != ok {
			t.Errorf("ValidSubPageURL(%q) = %v", raw, !ok)
		}
	}
	for raw, loops := range map[string]bool{
		"https://vpn.example.com/sub/{token}":      true,
		"https://VPN.example.com/Sub/x":            true,
		"https://vpn.example.com/cabinet/{token}":  false,
		"https://site.example.com/sub/{token}":     false,
		"https://vpn.example.com/subscription/{t}": false,
	} {
		if SubPageLoops(raw, "vpn.example.com", "sub") != loops {
			t.Errorf("SubPageLoops(%q) = %v", raw, !loops)
		}
	}
	if !SubPageLoops("https://vpn.example.com/sub/x", "vpn.example.com:8443", "sub") {
		t.Error("a host with a port was not matched")
	}
	s := &Settings{SubPageURL: "https://example.com/c?sub={token}"}
	if got := s.SubPageRedirect("abc"); got != "https://example.com/c?sub=abc" {
		t.Errorf("redirect = %q", got)
	}
}
