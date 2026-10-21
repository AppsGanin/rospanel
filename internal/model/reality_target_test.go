package model

import "testing"

// The server's own domain as the donor is reached on loopback; any other donor on its
// own :443.
func TestRealityTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		set  Settings
		want string
	}{
		{"donor", Settings{RealityDest: "max.ru", Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443}, "max.ru:443"},
		{"own domain", Settings{RealityDest: "VPN.example.com,max.ru", Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443}, "127.0.0.1:443"},
		{"own domain, other port", Settings{RealityDest: "vpn.example.com", Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 8443}, "127.0.0.1:8443"},
		{"own domain second", Settings{RealityDest: "max.ru,vpn.example.com", Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443}, "max.ru:443"},
		{"address only", Settings{RealityDest: "max.ru", Host: "203.0.113.5", SNI: "203.0.113.5", VLESSPort: 443}, "max.ru:443"},
	}
	for _, c := range cases {
		if got := c.set.RealityTarget(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
