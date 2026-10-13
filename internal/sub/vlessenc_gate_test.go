package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A VLESS Encryption inbound goes only to clients whose core speaks it; the rest of
// the custom inbounds go to everyone.
func TestVLESSEncOnlyToClientsThatSpeakIt(t *testing.T) {
	t.Parallel()
	set := &model.Settings{Host: "vpn.example.com", SNI: "vpn.example.com"}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}
	enc := model.Inbound{ID: 1, Enabled: true, Name: "enc", Protocol: model.InbVLESS, Port: 2001,
		Opts: model.InboundOpts{Transport: model.TrTCP, Security: model.SecNone, EncMode: model.EncNative,
			Decryption: "mlkem768x25519plus.native.600s.x", Encryption: "mlkem768x25519plus.native.0rtt.y"}}
	plain := model.Inbound{ID: 2, Enabled: true, Name: "plain", Protocol: model.InbVLESS, Port: 2002,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecNone, Path: "/ws"}}
	servers := []Server{{Set: set, Custom: []model.Inbound{enc, plain}, Access: model.UnrestrictedAccess()}}
	has := func(links []string, port string) bool {
		for _, l := range links {
			if strings.Contains(l, ":"+port+"?") {
				return true
			}
		}
		return false
	}
	for ua, want := range map[string]bool{
		"": true, "v2rayN/7.14.1": true, "v2rayN/7.14.0": false, "v2rayNG/1.10.21": true, "v2rayNG/1.10.20": false,
		"Exclave/0.16.2": true, "Exclave/0.16.1": false, "Throne/1.1.0": true, "husi/0.14.2 (sing-box 1.13.4)": true,
		"Shadowrocket/2070 CFNetwork/1410.0.3 Darwin/22.6.0": true, "Happ/3.0": false, "Streisand/1.6.76": false,
	} {
		links := ShareLinksFor(u, servers, ua)
		if got := has(links, "2001"); got != want {
			t.Errorf("%q: encrypted inbound %v, want %v", ua, got, want)
		}
		if !has(links, "2002") {
			t.Errorf("%q: the plain inbound was dropped", ua)
		}
	}
	for ua, want := range map[string]bool{
		"clash.meta/v1.19.14": true, "clash.meta/v1.19.13": false, "clash-verge/v2.3.0": false,
		"ClashMetaForAndroid/2.11.33": true,
	} {
		y := ClashYAMLFor(u, servers, ua)
		if got := strings.Contains(y, "port: 2001"); got != want {
			t.Errorf("%q: encrypted inbound in Clash %v, want %v", ua, got, want)
		}
		if !strings.Contains(y, "port: 2002") {
			t.Errorf("%q: the plain inbound was dropped from Clash", ua)
		}
	}
}

// A pinned inbound's name is anyone's: only a client that checks the fingerprint can
// use it, so the Clash profile keeps it from the sing-box readers of that format.
func TestPinnedInboundClashReaders(t *testing.T) {
	t.Parallel()
	set := &model.Settings{Host: "vpn.example.com", SNI: "vpn.example.com", CertSHA256: "ab12"}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}
	pin := model.Inbound{ID: 1, Enabled: true, Name: "pin", Protocol: model.InbVLESS, Port: 2001,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/ws", SNI: "www.example.org", PinCert: true}}
	servers := []Server{{Set: set, Custom: []model.Inbound{pin}, Access: model.UnrestrictedAccess()}}
	for ua, want := range map[string]bool{
		"clash.meta/v1.19.32": true,
		"HiddifyNext/4.1.1 (android) like ClashMeta v2ray sing-box": false,
		"NekoBox/Android/1.4.0 (Prefer ClashMeta Format)":           false,
	} {
		if got := strings.Contains(ClashYAMLFor(u, servers, ua), "port: 2001"); got != want {
			t.Errorf("%q: pinned inbound %v, want %v", ua, got, want)
		}
	}
}
