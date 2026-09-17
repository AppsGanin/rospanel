package model

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func wgKey(b byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(rune(b)), 32)))
}

func wgInbound() Inbound {
	return Inbound{
		ID: 7, Enabled: true, Name: "Calls", Protocol: InbWireGuard, Port: 56000,
		Opts: InboundOpts{Transport: TrTURN, Security: SecNone, WGPrivateKey: wgKey('a'), WGPublicKey: wgKey('b'), WGLocalPort: 20001,
			TurnMaskKey: strings.Repeat("ab", 32)},
	}
}

func fieldCode(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Code
	}
	return ""
}

// A WireGuard inbound's transport and security are its own, whatever was submitted, and
// nothing another protocol owns survives on it — nor the other way round.
func TestWireGuardNormalize(t *testing.T) {
	in := wgInbound()
	in.Opts.Transport, in.Opts.Security, in.Opts.SNI, in.Opts.Path = "ws", "tls", "x.example", "/p"
	in.Opts.Method, in.Opts.ShadowKey, in.Opts.HopEnd, in.Opts.Obfs = SS2022AES128, "k", 60000, "salamander"
	in.Opts.TurnLink = "  https://vk.com/call/join/abc  "
	in.Normalize()
	o := in.Opts
	if o.Transport != TrTURN || o.Security != SecNone {
		t.Errorf("transport/security = %s/%s", o.Transport, o.Security)
	}
	if o.SNI != "" || o.Path != "" || o.Method != "" || o.ShadowKey != "" || o.HopEnd != 0 || o.Obfs != "" {
		t.Errorf("foreign fields survived: %+v", o)
	}
	if o.TurnLink != "https://vk.com/call/join/abc" || o.WGLocalPort != 20001 || o.WGPublicKey == "" {
		t.Errorf("own fields lost: %+v", o)
	}
	if err := in.Validate(); err != nil {
		t.Errorf("a normalized inbound does not validate: %v", err)
	}

	for _, protocol := range []string{InbVLESS, InbHysteria, InbShadowsocks} {
		other := Inbound{Name: "x", Protocol: protocol, Port: 1,
			Opts: InboundOpts{WGPrivateKey: wgKey('a'), WGPublicKey: wgKey('b'), WGLocalPort: 5, TurnLink: "https://vk.com/call/join/a", TurnMaskKey: strings.Repeat("ab", 32)}}
		other.Normalize()
		if other.Opts.WGPrivateKey != "" || other.Opts.WGPublicKey != "" || other.Opts.WGLocalPort != 0 || other.Opts.TurnLink != "" || other.Opts.TurnMaskKey != "" {
			t.Errorf("%s kept WireGuard fields: %+v", protocol, other.Opts)
		}
	}
}

func TestWireGuardValidate(t *testing.T) {
	cases := map[string]struct {
		mut  func(*Inbound)
		code string
	}{
		"ok":                    {func(*Inbound) {}, ""},
		"vk link":               {func(in *Inbound) { in.Opts.TurnLink = "https://vk.com/call/join/abc" }, ""},
		"telemost link":         {func(in *Inbound) { in.Opts.TurnLink = "https://telemost.yandex.ru/j/123" }, "err.turnLinkBad"},
		"no mask key":           {func(in *Inbound) { in.Opts.TurnMaskKey = "" }, "err.wgKeyBad"},
		"no key":                {func(in *Inbound) { in.Opts.WGPrivateKey = "" }, "err.wgKeyBad"},
		"short public key":      {func(in *Inbound) { in.Opts.WGPublicKey = "AAAA" }, "err.wgKeyBad"},
		"no local port":         {func(in *Inbound) { in.Opts.WGLocalPort = 0 }, "err.wgLocalPortBad"},
		"local port is relay's": {func(in *Inbound) { in.Opts.WGLocalPort = in.Port }, "err.wgLocalPortBad"},
		"http link":             {func(in *Inbound) { in.Opts.TurnLink = "http://vk.com/call/join/abc" }, "err.turnLinkBad"},
		"foreign host":          {func(in *Inbound) { in.Opts.TurnLink = "https://vk.com.evil.example/call" }, "err.turnLinkBad"},
		"credentials in link":   {func(in *Inbound) { in.Opts.TurnLink = "https://a:b@vk.com/call/join/x" }, "err.turnLinkBad"},
		"too long":              {func(in *Inbound) { in.Opts.TurnLink = "https://vk.com/" + strings.Repeat("a", TurnLinkMaxLen) }, "err.turnLinkBad"},
	}
	for name, c := range cases {
		in := wgInbound()
		c.mut(&in)
		err := in.Validate()
		if got := fieldCode(err); got != c.code || (c.code == "" && err != nil) {
			t.Errorf("%s: err %v (code %q), want code %q", name, err, got, c.code)
		}
	}
}

func TestValidTurnLink(t *testing.T) {
	for link, want := range map[string]bool{
		"https://vk.com/call/join/abc":         true,
		"https://VK.ru/call/join/abc":          true,
		"https://vk.me/join/abc":               true,
		"https://vk.com:8443/call/join/abc":    true,
		"https://telemost.yandex.ru/j/1":       false, // no client the panel offers takes it
		"https://example.com/call":             false,
		"vk.com/call/join/abc":                 false,
		"":                                     false,
		"javascript:alert(1)//vk.com/call/abc": false,
	} {
		if got := ValidTurnLink(link); got != want {
			t.Errorf("ValidTurnLink(%q) = %v, want %v", link, got, want)
		}
	}
}

// The relay listens on UDP, and the loopback listener behind it holds a second UDP port
// on the same box: both are refused where taken, on UDP only.
func TestWireGuardPortsInTheSet(t *testing.T) {
	wg := wgInbound()
	if ProtoOf(wg.Protocol) != "udp" {
		t.Fatalf("ProtoOf(wireguard) = %s", ProtoOf(wg.Protocol))
	}
	if f := wg.UnsupportedFormats(); f != nil {
		t.Errorf("UnsupportedFormats = %v, want none (the lane is in no format)", f)
	}

	hy := Inbound{ID: 8, Enabled: true, Name: "hy", Protocol: InbHysteria, Port: wg.Opts.WGLocalPort,
		Opts: InboundOpts{Transport: TrHysteria, Security: SecTLS}}
	if code := fieldCode(ValidateInboundSet([]Inbound{wg, hy}, NewReservedPorts(), nil)); code != "err.portTakenByInbound" {
		t.Errorf("a UDP inbound on the loopback port: code %q", code)
	}
	tcp := Inbound{ID: 9, Enabled: true, Name: "tcp", Protocol: InbVLESS, Port: wg.Opts.WGLocalPort,
		Opts: InboundOpts{Transport: TrWS, Security: SecTLS, Path: "/p"}}
	if err := ValidateInboundSet([]Inbound{wg, tcp}, NewReservedPorts(), nil); err != nil {
		t.Errorf("a TCP inbound on the same number is no collision: %v", err)
	}
	reserved := NewReservedPorts()
	reserved.HoldUDP(wg.Opts.WGLocalPort, "AmneziaWG")
	if code := fieldCode(ValidateInboundSet([]Inbound{wg}, reserved, nil)); code != "err.portTakenBy" {
		t.Errorf("a reserved UDP port as the loopback port: code %q", code)
	}
	second := wgInbound()
	second.ID, second.Name, second.Port = 10, "Calls 2", wg.Opts.WGLocalPort
	second.Opts.WGLocalPort = 20002
	if code := fieldCode(ValidateInboundSet([]Inbound{wg, second}, NewReservedPorts(), nil)); code != "err.portTakenByInbound" {
		t.Errorf("a relay on another inbound's loopback port: code %q", code)
	}
}
