package model

import "testing"

// The server's own domain as the donor is reached on loopback; any other donor on its
// own :443. A lane sharing the TCP-TLS lane's port never dials loopback: its own name
// is served by the decoy there, so the front could not tell the two apart.
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
		{"shared 443, donor", Settings{RealityDest: "max.ru", Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443, RealityPort: 443}, "max.ru:443"},
		{"shared 443, own name", Settings{RealityDest: "vpn.example.com", Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443, RealityPort: 443}, "vpn.example.com:443"},
	}
	for _, c := range cases {
		if got := c.set.RealityTarget(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A shared port is asked for by giving REALITY the TCP-TLS lane's port.
func TestRealityShared(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		set  Settings
		want bool
	}{
		{Settings{VLESSPort: 443, RealityPort: 8443}, false},
		{Settings{VLESSPort: 443, RealityPort: 443}, true},
		{Settings{VLESSPort: 0, RealityPort: 0}, false},
	} {
		if got := c.set.RealityShared(); got != c.want {
			t.Errorf("%d/%d: %v, want %v", c.set.VLESSPort, c.set.RealityPort, got, c.want)
		}
	}
}

// The own name cannot be REALITY's mask on a shared port; on a lane with its own port
// it is exactly the intended one.
func TestRealityShareConflict(t *testing.T) {
	t.Parallel()
	own := Settings{RealityDest: "max.ru,VPN.example.com", Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443}
	if got := own.RealityShareConflict(); got != "" {
		t.Fatalf("own port: %q, want no conflict", got)
	}
	own.RealityPort = 443
	if got := own.RealityShareConflict(); got != "VPN.example.com" {
		t.Fatalf("shared: %q, want the own name", got)
	}
	own.RealityDest = "max.ru"
	if got := own.RealityShareConflict(); got != "" {
		t.Fatalf("donor only: %q, want no conflict", got)
	}
	ip := Settings{RealityDest: "max.ru", Host: "203.0.113.5", SNI: "203.0.113.5", VLESSPort: 443, RealityPort: 443}
	if got := ip.RealityShareConflict(); got != "" {
		t.Fatalf("an address has no name to conflict with: %q", got)
	}
}
