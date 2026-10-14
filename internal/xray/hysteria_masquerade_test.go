package xray

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Every Hysteria2 inbound answers a stranger's HTTP/3 request with the decoy, not
// Go's bare 404 — the built-in lane and a custom one alike.
func TestHysteriaInboundsMasqueradeAsTheDecoy(t *testing.T) {
	hy := model.Inbound{ID: 7, Enabled: true, Name: "H", Protocol: model.InbHysteria, Port: 7000}
	hy.Normalize()
	cfg, err := Generate(baseSettings(), nil, Options{PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{hy}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{TagHysteria, hy.Tag()} {
		in := findInbound(cfg, tag)
		if in == nil || in.StreamSettings == nil || in.StreamSettings.HysteriaSettings == nil {
			t.Fatalf("%s: no hysteria settings", tag)
		}
		m := in.StreamSettings.HysteriaSettings.Masquerade
		if m == nil || m.Type != "proxy" || m.URL != "http://"+DecoyH3Addr {
			t.Errorf("%s: masquerade = %+v", tag, m)
		}
	}
}
