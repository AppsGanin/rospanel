package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

const nyame = "nekobox/5.11.28.3 (Prefer ClashMeta Format)"

// NyameBox from 5.11.28 is served the link list; NekoBox for Android and older NyameBox
// are not.
func TestNyameBoxLinkList(t *testing.T) {
	t.Parallel()
	for ua, want := range map[string]bool{
		nyame: true,
		"nekobox/5.11.28.7 (Prefer ClashMeta Format)":     true,
		"nekobox/5.11.24 (Prefer ClashMeta Format)":       false,
		"NekoBox/Android/1.4.2 (Prefer ClashMeta Format)": false,
	} {
		if got := NyameBoxLinkList(ua); got != want {
			t.Errorf("NyameBoxLinkList(%q) = %v, want %v", ua, got, want)
		}
	}
}

// NyameBox gets TrustTunnel in its URL form, naive with uot, AnyTLS, the hop range as
// mport, insecure=1 for the server's self-signed pin and no borrowed-SNI pinned inbound.
func TestNyameBoxLinks(t *testing.T) {
	set := testSet("vpn.example.com")
	set.NaiveEnabled, set.AnyTLSEnabled, set.MieruEnabled, set.MieruPort, set.TrustTunnelEnabled = true, true, true, 2999, true
	set.HysteriaObfs, set.HopStart, set.HopEnd = "obfspw", 20000, 20100
	set.TLSPinSHA256 = "abcd"
	set.CertSHA256 = "ef01"
	u := model.User{ID: 7, Name: "u", UUID: "11111111-2222-3333-4444-555555555555", Password: "pw"}
	pinned := model.Inbound{ID: 1, Enabled: true, Name: "pinned", Protocol: model.InbVLESS, Port: 9001,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/w", PinCert: true, SNI: "max.ru"}}
	plain := model.Inbound{ID: 2, Enabled: true, Name: "plain", Protocol: model.InbTrojan, Port: 9002,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/t"}}
	servers := []Server{{Set: set, Custom: []model.Inbound{pinned, plain}, Access: model.UnrestrictedAccess()}}

	all := strings.Join(ShareLinksFor(u, servers, nyame), "\n")
	for _, want := range []string{
		"tt://tt-u7:11111111-2222-3333-4444-555555555555@vpn.example.com:443?alpn=h2&sni=vpn.example.com#",
		"naive+https://", "uot=1", "anytls://", "mport=20000-20100", "#plain",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in:\n%s", want, all)
		}
	}
	for _, bad := range []string{"tt://?", "mierus://", "fm=", "#pinned"} {
		if strings.Contains(all, bad) {
			t.Errorf("%q in:\n%s", bad, all)
		}
	}
	for _, l := range strings.Split(all, "\n") {
		if strings.Contains(l, "pcs=") && !strings.Contains(l, "insecure=1") {
			t.Errorf("a pin NyameBox cannot read without insecure=1: %s", l)
		}
	}
	if !strings.Contains(strings.Join(ShareLinksFor(u, servers, "v2rayNG/1.10.30"), "\n"), "#pinned") {
		t.Error("the pinned inbound left out for v2rayNG too")
	}
}
