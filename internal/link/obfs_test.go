package link

import (
	"net/url"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func obfsSettings() *model.Settings {
	return &model.Settings{
		Host: "vpn.example.com", SNI: "vpn.example.com",
		HysteriaPort: 443, HopStart: 443, HopEnd: 443, HopInterval: "5-10",
	}
}

// fmValue pulls the fm parameter back out of a link and undoes the extra escaping
// the link applies, giving the JSON document a client would parse.
func fmValue(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	v := u.Query().Get("fm")
	if v == "" {
		return ""
	}
	dec, err := url.QueryUnescape(v)
	if err != nil {
		t.Fatalf("unescape fm %q: %v", v, err)
	}
	return dec
}

// A lane with neither hopping nor obfuscation carries no fm at all — the parameter
// exists to describe packet shaping, and an empty document would only be noise for
// every client to parse.
func TestHysteriaLinkNoFinalMaskWhenPlain(t *testing.T) {
	raw := Hysteria2(model.User{Password: "pw"}, obfsSettings())
	if fm := fmValue(t, raw); fm != "" {
		t.Errorf("fm = %q, want none", fm)
	}
	if strings.Contains(raw, "obfs") {
		t.Errorf("link mentions obfs with no key set: %s", raw)
	}
}

// Hopping alone must produce exactly the document it always did. Clients parse this
// blob; a reordered or renamed key here is a lane that stops hopping in the field
// while every test that only checks "fm is present" stays green.
func TestHysteriaLinkHopOnlyDocumentUnchanged(t *testing.T) {
	set := obfsSettings()
	set.HopEnd = 450
	got := fmValue(t, Hysteria2(model.User{Password: "pw"}, set))
	const want = `{"quicParams":{"udpHop":{"ports":"443-450","interval":"5-10"},"congestion":"bbr"}}`
	if got != want {
		t.Errorf("fm = %s\nwant  %s", got, want)
	}
}

// With a key the link has to satisfy two client families at once: Xray-core clients
// read the mask out of fm, while sing-box, mihomo and the reference hysteria client
// read obfs/obfs-password. The two implementations are wire compatible, so one link
// carries both rather than the panel guessing which app the user installed.
func TestHysteriaLinkCarriesObfsForBothClientFamilies(t *testing.T) {
	set := obfsSettings()
	set.HysteriaObfs = "abcdefgh12345678"
	raw := Hysteria2(model.User{Password: "pw"}, set)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := u.Query()
	if q.Get("obfs") != "salamander" || q.Get("obfs-password") != "abcdefgh12345678" {
		t.Errorf("obfs params = %q/%q", q.Get("obfs"), q.Get("obfs-password"))
	}
	const want = `{"udp":[{"type":"salamander","settings":{"password":"abcdefgh12345678"}}]}`
	if got := fmValue(t, raw); got != want {
		t.Errorf("fm = %s\nwant  %s", got, want)
	}
}

// Both at once: the mask and the hop window share one document, in that order.
func TestHysteriaLinkObfsAndHopTogether(t *testing.T) {
	set := obfsSettings()
	set.HopEnd, set.HysteriaObfs = 450, "abcdefgh12345678"
	const want = `{"udp":[{"type":"salamander","settings":{"password":"abcdefgh12345678"}}],` +
		`"quicParams":{"udpHop":{"ports":"443-450","interval":"5-10"},"congestion":"bbr"}}`
	if got := fmValue(t, Hysteria2(model.User{Password: "pw"}, set)); got != want {
		t.Errorf("fm = %s\nwant  %s", got, want)
	}
}

// A custom Hysteria2 inbound takes its key from the inbound, not from the lane.
func TestCustomHysteriaLinkCarriesItsOwnObfs(t *testing.T) {
	set := obfsSettings()
	set.HysteriaObfs = "lane-key-abcdef"
	in := model.Inbound{
		ID: 3, Name: "custom", Protocol: model.InbHysteria, Port: 8443,
		Opts: model.InboundOpts{Transport: model.TrHysteria, Security: model.SecTLS, Obfs: "inbound-key-12"},
	}
	u, err := url.Parse(Custom(model.User{Password: "pw"}, in, set))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := u.Query().Get("obfs-password"); got != "inbound-key-12" {
		t.Errorf("obfs-password = %q, want the inbound's own key", got)
	}
}
