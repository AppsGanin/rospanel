package model

import "testing"

func TestIsTunnelIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"10.66.0.2": true, "10.66.255.254": true,
		"10.65.0.2": false, "10.6.0.2": false, "110.66.0.2": false,
		"178.176.163.245": false, "": false, "2001:db8::1": false,
	} {
		if got := IsTunnelIP(ip); got != want {
			t.Errorf("IsTunnelIP(%q) = %v, want %v", ip, got, want)
		}
	}
}

func TestTunnelVia(t *testing.T) {
	list := []Inbound{
		{ID: 1, Protocol: InbTurnVK, Enabled: true},
		{ID: 2, Protocol: InbWireGuard, Enabled: true},
		{ID: 3, Protocol: InbWireGuard, Enabled: false},
		{ID: 4, Protocol: InbVLESS, Enabled: true},
	}
	if got := TunnelVia(list, UnrestrictedAccess()); len(got) != 2 || got[0] != "WG" || got[1] != "TURN-VK" {
		t.Errorf("unrestricted: %v", got)
	}
	only1 := Access{Tokens: map[string]bool{InboundToken(1): true}}
	if got := TunnelVia(list, only1); len(got) != 1 || got[0] != "TURN-VK" {
		t.Errorf("only the TURN inbound: %v", got)
	}
	only3 := Access{Tokens: map[string]bool{InboundToken(3): true, InboundToken(4): true}}
	if got := TunnelVia(list, only3); got != nil {
		t.Errorf("a disabled inbound or another protocol named: %v", got)
	}
}
