package sub

import (
	"net/url"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Hiddify 5 is served the link list with the lanes its link parser reads; 4.x keeps
// the Clash profile and none of them.
func TestHiddifyLinkList(t *testing.T) {
	t.Parallel()
	const (
		v5 = "HiddifyNext/5.5.5 (android) like ClashMeta v2ray sing-box"
		v4 = "HiddifyNext/4.1.1 (android) like ClashMeta v2ray sing-box"
	)
	for _, c := range []struct {
		name string
		fn   func(string) bool
		ua   string
		want bool
	}{
		{"5.5.5 list", HiddifyLinkList, v5, true},
		{"5.9.1 list", HiddifyLinkList, "HiddifyNext/5.9.1 (windows) like ClashMeta v2ray sing-box", true},
		{"5.4.0 clash", HiddifyLinkList, "HiddifyNext/5.4.0 (android) like ClashMeta v2ray sing-box", false},
		{"4.x clash", HiddifyLinkList, v4, false},
		{"naive", func(ua string) bool { return NaiveLinkFor(ua) != nil }, v5, true},
		{"anytls", AnyTLSLinkFor, v5, true},
		{"mieru", MieruLinkFor, v5, true},
		{"no trusttunnel", TrustTunnelLinkFor, v5, false},
		{"no awg 3.1", func(ua string) bool { return AWGLinkFor(ua) }, v5, false},
		{"4.x no mieru", MieruLinkFor, v4, false},
	} {
		if got := c.fn(c.ua); got != c.want {
			t.Errorf("%s (%q) = %v, want %v", c.name, c.ua, got, c.want)
		}
	}
}

// Hiddify's hysteria2:// has no fm (its parser drops a line whose fm it cannot read),
// the hop range as its port and insecure=1 in place of a pin; the other clients keep
// Xray's form.
func TestHiddifyHysteria2Link(t *testing.T) {
	set := testSet("203.0.113.5")
	set.SNI = "203.0.113.5"
	set.HysteriaObfs, set.HopStart, set.HopEnd = "obfspw", 20000, 20100
	set.TLSPinSHA256 = "abcd"
	u := model.User{ID: 7, Name: "u", UUID: "11111111-2222-3333-4444-555555555555", Password: "p@ss"}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}

	hy2 := func(links []string) string {
		for _, l := range links {
			if strings.HasPrefix(l, "hysteria2://") {
				return l
			}
		}
		t.Fatal("no hysteria2 link")
		return ""
	}
	h := hy2(ShareLinksFor(u, servers, "HiddifyNext/5.9.1 (android) like ClashMeta v2ray sing-box"))
	// A port range is not a URL port: read the rest with the first port, as ray2sing does.
	if !strings.Contains(h, "@203.0.113.5:20000-20100?") {
		t.Fatalf("no hop range as the port: %s", h)
	}
	p, err := url.Parse(strings.Replace(h, ":20000-20100?", ":20000?", 1))
	if err != nil {
		t.Fatal(err)
	}
	q := p.Query()
	if q.Has("fm") || q.Get("insecure") != "1" ||
		q.Get("obfs") != "salamander" || q.Get("obfs-password") != "obfspw" {
		t.Errorf("Hiddify hysteria2 link: %s", h)
	}
	if pw, _ := url.QueryUnescape(p.User.Username()); pw != "p@ss" || p.Fragment == "" {
		t.Errorf("credentials or label lost: %s", h)
	}
	if x := hy2(ShareLinksFor(u, servers, "v2rayNG/1.10.30")); !strings.Contains(x, "fm=") || strings.Contains(x, "insecure") {
		t.Errorf("Xray form changed for v2rayNG: %s", x)
	}
}

// Hiddify reads a VLESS pin but not a Hysteria2 one: a Hysteria2 inbound pinned under a
// borrowed SNI is left out, a VLESS one kept.
func TestHiddifyPinnedInbounds(t *testing.T) {
	set := testSet("vpn.example.com")
	set.CertSHA256 = "ef01"
	u := model.User{ID: 7, Name: "u", UUID: "11111111-2222-3333-4444-555555555555", Password: "pw"}
	opts := model.InboundOpts{Security: model.SecTLS, PinCert: true, SNI: "max.ru"}
	hy := model.Inbound{ID: 1, Enabled: true, Name: "hy-pinned", Protocol: model.InbHysteria, Port: 9001, Opts: opts}
	opts.Transport, opts.Path = model.TrWS, "/w"
	vl := model.Inbound{ID: 2, Enabled: true, Name: "vless-pinned", Protocol: model.InbVLESS, Port: 9002, Opts: opts}
	servers := []Server{{Set: set, Custom: []model.Inbound{hy, vl}, Access: model.UnrestrictedAccess()}}
	all := strings.Join(ShareLinksFor(u, servers, "HiddifyNext/5.9.1 (android) like ClashMeta v2ray sing-box"), "\n")
	if strings.Contains(all, "#hy-pinned") || !strings.Contains(all, "#vless-pinned") {
		t.Errorf("pinned inbounds for Hiddify:\n%s", all)
	}
}

// A rule that hands Hiddify the Xray JSON profile builds it from the Xray form of the
// links, not from the rewrite for Hiddify's own parser: Hysteria2 keeps its lane.
func TestHiddifyXrayJSONUsesXrayLinks(t *testing.T) {
	set := testSet("vpn.example.com")
	set.HysteriaObfs, set.HopStart, set.HopEnd = "obfspw", 20000, 20100
	u := model.User{ID: 7, Name: "u", UUID: "11111111-2222-3333-4444-555555555555", Password: "pw"}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}
	ua := "HiddifyNext/5.9.1 (android) like ClashMeta v2ray sing-box"
	got, err := XrayJSONRule(u, servers, model.SubDPI{}, "", ua, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := XrayJSONMulti(u, servers, model.SubDPI{})
	if got != want {
		t.Errorf("Xray JSON for Hiddify differs from the plain one:\n%s\n---\n%s", got, want)
	}
	if !strings.Contains(got, "hysteria") {
		t.Errorf("no Hysteria2 in the Xray JSON:\n%s", got)
	}
}
