package link

import (
	"net"
	"net/url"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// An IPv6 server address goes into links in brackets. Go's url.Parse splits a bare
// "2001:db8::1:443" at the last colon, but clients do not (java.net.URI in Android
// apps reads no host at all), so the authority must split cleanly on its own.
func TestLinksBracketIPv6Host(t *testing.T) {
	set := &model.Settings{Host: "2001:db8::1", SNI: "vpn.example.com", VLESSPort: 443}
	u := model.User{UUID: "11111111-2222-3333-4444-555555555555", Password: "pw"}
	ss := model.Inbound{Protocol: model.InbShadowsocks, Port: 8388, Name: "ss",
		Opts: model.InboundOpts{Method: "2022-blake3-aes-128-gcm", ShadowKey: "c2VydmVyLWtleS0xNmJ5dA=="}}
	trojan := model.Inbound{Protocol: model.InbTrojan, Port: 8443, Name: "tr"}
	for name, raw := range map[string]string{
		"vless": VLESS(u, set), "ss": Custom(u, ss, set), "trojan": Custom(u, trojan, set),
	} {
		p, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %q does not parse: %v", name, raw, err)
		}
		host, port, err := net.SplitHostPort(p.Host)
		if err != nil || host != "2001:db8::1" || port == "" {
			t.Errorf("%s: authority %q in %q (host %q, port %q, %v)", name, p.Host, raw, host, port, err)
		}
	}
}
