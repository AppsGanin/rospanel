package model

import (
	"strings"
	"testing"
)

func TestCalculateTenantSpeed(t *testing.T) {
	tests := []struct {
		name          string
		totalSpeed    int
		activeTenants int
		want          int
	}{
		{"unlimited", 0, 5, 0},
		{"negative speed", -100, 3, 0},
		{"zero tenants", 10000, 0, 10000},
		{"single tenant", 10000, 1, 10000},
		{"even division", 100000, 4, 25000},
		{"odd division", 100000, 3, 33333},
		{"clamped minimum floor", 100, 10, 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateTenantSpeed(tt.totalSpeed, tt.activeTenants)
			if got != tt.want {
				t.Errorf("CalculateTenantSpeed(%d, %d) = %d, want %d", tt.totalSpeed, tt.activeTenants, got, tt.want)
			}
		})
	}
}

func TestCalculateTenantQuota(t *testing.T) {
	tests := []struct {
		name          string
		totalQuota    int
		activeTenants int
		want          int
	}{
		{"default/zero quota", 0, 4, 100},
		{"single tenant", 80, 1, 80},
		{"even division", 90, 3, 30},
		{"odd division", 100, 3, 33},
		{"clamped minimum floor", 10, 20, 1},
		{"over 100 single", 150, 1, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateTenantQuota(tt.totalQuota, tt.activeTenants)
			if got != tt.want {
				t.Errorf("CalculateTenantQuota(%d, %d) = %d, want %d", tt.totalQuota, tt.activeTenants, got, tt.want)
			}
		})
	}
}

func TestEncodeAndDecodeShareLink(t *testing.T) {
	payload := NodeSharePayload{
		Version:       1,
		NodeID:        42,
		Host:          "node42.example.com",
		Name:          "Germany Node",
		ShareToken:    "sec_token_1234567890abcdef",
		QuotaPercent:  50,
		SpeedLimit:    100000,
		ReservedPorts: []int{443, 8443, 10085},
		Protocols:     []string{"vless", "hysteria2", "reality"},
	}

	link, err := EncodeShareLink(payload)
	if err != nil {
		t.Fatalf("EncodeShareLink failed: %v", err)
	}

	if !strings.HasPrefix(link, ShareLinkPrefix) {
		t.Errorf("link does not have prefix %s: %s", ShareLinkPrefix, link)
	}

	decoded, err := DecodeShareLink(link)
	if err != nil {
		t.Fatalf("DecodeShareLink failed: %v", err)
	}

	if decoded.NodeID != payload.NodeID {
		t.Errorf("NodeID = %d, want %d", decoded.NodeID, payload.NodeID)
	}
	if decoded.Host != payload.Host {
		t.Errorf("Host = %q, want %q", decoded.Host, payload.Host)
	}
	if decoded.Name != payload.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, payload.Name)
	}
	if decoded.ShareToken != payload.ShareToken {
		t.Errorf("ShareToken = %q, want %q", decoded.ShareToken, payload.ShareToken)
	}
	if decoded.QuotaPercent != payload.QuotaPercent {
		t.Errorf("QuotaPercent = %d, want %d", decoded.QuotaPercent, payload.QuotaPercent)
	}
	if decoded.SpeedLimit != payload.SpeedLimit {
		t.Errorf("SpeedLimit = %d, want %d", decoded.SpeedLimit, payload.SpeedLimit)
	}
	if len(decoded.ReservedPorts) != len(payload.ReservedPorts) {
		t.Errorf("ReservedPorts length = %d, want %d", len(decoded.ReservedPorts), len(payload.ReservedPorts))
	}
}

func TestDecodeShareLinkErrors(t *testing.T) {
	// Empty link
	if _, err := DecodeShareLink(""); err == nil {
		t.Error("DecodeShareLink('') want error, got nil")
	}

	// Corrupt base64
	if _, err := DecodeShareLink("rpnshare://!@#not-valid-base64"); err == nil {
		t.Error("DecodeShareLink(corrupt) want error, got nil")
	}

	// Bad json
	badJSONLink := ShareLinkPrefix + "e30" // "{}"
	if _, err := DecodeShareLink(badJSONLink); err == nil {
		t.Error("DecodeShareLink(empty json) want error, got nil")
	}

	// Missing fields
	badPayload := NodeSharePayload{
		Version:    1,
		NodeID:     0, // missing ID
		Host:       "example.com",
		ShareToken: "token",
	}
	encodedBad, _ := EncodeShareLink(badPayload)
	if _, err := DecodeShareLink(encodedBad); err == nil {
		t.Error("DecodeShareLink(missing node id) want error, got nil")
	}
}

func TestEncodeShareLinkErrors(t *testing.T) {
	// Missing host
	if _, err := EncodeShareLink(NodeSharePayload{NodeID: 1, ShareToken: "token"}); err == nil {
		t.Error("EncodeShareLink(missing host) want error, got nil")
	}
	// Missing token
	if _, err := EncodeShareLink(NodeSharePayload{NodeID: 1, Host: "example.com"}); err == nil {
		t.Error("EncodeShareLink(missing token) want error, got nil")
	}
}
