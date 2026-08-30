package happ

import (
	"encoding/base64"
	"testing"
)

func TestDecodePlainAndBase64(t *testing.T) {
	sample := "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=tls#TestVLESS\n" +
		"trojan://password123@example.com:443#TestTrojan\n"

	// 1. Plain text
	lines := Decode([]byte(sample))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	// 2. Base64
	b64 := base64.StdEncoding.EncodeToString([]byte(sample))
	linesB64 := Decode([]byte(b64))
	if len(linesB64) != 2 {
		t.Fatalf("expected 2 lines from base64, got %d", len(linesB64))
	}

	// 3. Base64 with newlines
	b64WithNewlines := b64[:10] + "\n" + b64[10:]
	linesB64NL := Decode([]byte(b64WithNewlines))
	if len(linesB64NL) != 2 {
		t.Fatalf("expected 2 lines from base64 with newlines, got %d", len(linesB64NL))
	}

	// 4. URL-safe Base64
	b64URL := base64.URLEncoding.EncodeToString([]byte(sample))
	linesB64URL := Decode([]byte(b64URL))
	if len(linesB64URL) != 2 {
		t.Fatalf("expected 2 lines from base64 URL, got %d", len(linesB64URL))
	}
}

func TestParseURIs(t *testing.T) {
	lines := []string{
		"# Comment line",
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?type=ws&security=tls&path=%2Fws#Netherlands-VLESS",
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"de.example.com","port":443,"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","net":"ws","tls":"tls","ps":"Germany-VMess"}`)),
		"trojan://mypassword@fi.example.com:443?security=tls#Finland-Trojan",
		"ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:sspassword")) + "@se.example.com:8388#Sweden-SS",
		"hysteria2://hy2pass@no.example.com:443?sni=no.example.com#Norway-Hy2",
		"invalid://nothing",
	}

	nodes := ParseURIs(lines, 1)
	if len(nodes) != 5 {
		t.Fatalf("expected 5 parsed nodes, got %d", len(nodes))
	}

	// Verify VLESS
	if nodes[0].Protocol != "vless" || nodes[0].Host != "nl.example.com" || nodes[0].Port != 443 || nodes[0].Name != "Netherlands-VLESS" {
		t.Errorf("vless node mismatch: %+v", nodes[0])
	}
	if nodes[0].IdentityKey == "" {
		t.Errorf("vless identity key is empty")
	}

	// Verify VMess
	if nodes[1].Protocol != "vmess" || nodes[1].Host != "de.example.com" || nodes[1].Port != 443 || nodes[1].Name != "Germany-VMess" {
		t.Errorf("vmess node mismatch: %+v", nodes[1])
	}

	// Verify Trojan
	if nodes[2].Protocol != "trojan" || nodes[2].Host != "fi.example.com" || nodes[2].Port != 443 || nodes[2].Name != "Finland-Trojan" {
		t.Errorf("trojan node mismatch: %+v", nodes[2])
	}

	// Verify SS
	if nodes[3].Protocol != "ss" || nodes[3].Host != "se.example.com" || nodes[3].Port != 8388 || nodes[3].Name != "Sweden-SS" {
		t.Errorf("ss node mismatch: %+v", nodes[3])
	}

	// Verify Hysteria2
	if nodes[4].Protocol != "hysteria2" || nodes[4].Host != "no.example.com" || nodes[4].Port != 443 || nodes[4].Name != "Norway-Hy2" {
		t.Errorf("hy2 node mismatch: %+v", nodes[4])
	}
}

func TestParseURIsDeduplication(t *testing.T) {
	lines := []string{
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443#Name1",
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443#Name2", // duplicate endpoint
	}
	nodes := ParseURIs(lines, 1)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node after dedup, got %d", len(nodes))
	}
}
