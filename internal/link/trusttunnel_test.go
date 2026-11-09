package link

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// ttFields decodes a deep link's TLV payload, string values only, the repeated address
// tag kept in order.
func ttFields(t *testing.T, link string) map[uint64][]string {
	t.Helper()
	raw, ok := strings.CutPrefix(link, "tt://?")
	if !ok {
		t.Fatalf("not a tt:// link: %s", link)
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	varint := func() uint64 {
		n := 1 << (b[0] >> 6)
		v := uint64(b[0] & 0x3f)
		for i := 1; i < n; i++ {
			v = v<<8 | uint64(b[i])
		}
		b = b[n:]
		return v
	}
	out := map[uint64][]string{}
	for len(b) > 0 {
		tag, n := varint(), varint()
		out[tag] = append(out[tag], string(b[:n]))
		b = b[n:]
	}
	return out
}

func TestTrustTunnelLink(t *testing.T) {
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}
	set := &model.Settings{Host: "203.0.113.5", SNI: "vpn.example.com", VLESSPort: 443, TrustTunnelEnabled: true, NodeLabel: "NL"}
	if !TrustTunnelOn(set) {
		t.Fatal("lane off")
	}
	f := ttFields(t, TrustTunnel(u, set))
	want := map[uint64]string{
		ttTagVersion:  "\x02",
		ttTagHostname: "vpn.example.com",
		ttTagAddress:  "203.0.113.5:443",
		ttTagUsername: "tt-u7",
		ttTagPassword: u.UUID,
		ttTagProtocol: "\x01",
		ttTagName:     LabelFor(model.ProtoTrustTunnel, u, set),
	}
	for tag, v := range want {
		if len(f[tag]) != 1 || f[tag][0] != v {
			t.Errorf("tag %#x = %q, want %q", tag, f[tag], v)
		}
	}
	if len(f) != len(want) {
		t.Errorf("fields %v", f)
	}
	if TrustTunnelOn(&model.Settings{TrustTunnelEnabled: true, TLSInsecure: true}) {
		t.Fatal("handed out with a certificate no client trusts")
	}
	// The app cannot verify a certificate issued to an IP: no link for a server known
	// only by its address, one for an address with a domain to verify.
	if TrustTunnelOn(&model.Settings{Host: "203.0.113.5", TrustTunnelEnabled: true}) {
		t.Fatal("handed out for an IP certificate")
	}
	set6 := &model.Settings{Host: "2001:db8::1", SNI: "vpn.example.com", VLESSPort: 8443, TrustTunnelEnabled: true}
	if !TrustTunnelOn(set6) {
		t.Fatal("lane off for an IPv6 host with a domain")
	}
	ipv6 := ttFields(t, TrustTunnel(u, set6))
	if ipv6[ttTagAddress][0] != "[2001:db8::1]:8443" || ipv6[ttTagHostname][0] != "vpn.example.com" {
		t.Fatalf("IPv6 host: %v", ipv6)
	}
}

func TestQUICVarint(t *testing.T) {
	for v, want := range map[uint64]string{
		0: "\x00", 63: "\x3f", 64: "\x40\x40", 16383: "\x7f\xff", 16384: "\x80\x00\x40\x00",
	} {
		if got := string(appendQUICVarint(nil, v)); got != want {
			t.Errorf("%d: % x, want % x", v, got, want)
		}
	}
}
