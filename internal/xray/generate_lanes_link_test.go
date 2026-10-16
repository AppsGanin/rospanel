package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A lane whose upstream is another VPN server (a share link) gets an outbound
// built from the link — protocol, credentials and transport included — under the
// same tag scheme as a socks upstream, so the balancer and the Observatory see no
// difference between the two.
func TestLaneUpstreamMayBeAShareLink(t *testing.T) {
	rc := model.RoutingConfig{Lanes: []model.EgressLane{
		{ID: "partner", Name: "partner", Enabled: true, Domains: []string{"example.com"}},
	}}
	pool := map[string][]model.ProxyEndpoint{"partner": {
		{Protocol: "vless", Address: "9.9.9.9", Port: 443,
			Link: "vless://11111111-2222-3333-4444-555555555555@9.9.9.9:443?type=tcp&security=reality&sni=max.ru&pbk=PK&sid=ab&flow=xtls-rprx-vision#p"},
		ep("10.0.0.1"),
		{Protocol: "vless", Address: "x", Port: 1, Link: "vless://broken"}, // unreadable: skipped, not guessed at
	}}
	cfg := genLanes(t, rc, pool)
	tags := outboundTags(cfg)
	var linkOut, socksOut *Outbound
	for i := range cfg.Outbounds {
		switch cfg.Outbounds[i].Tag {
		case "proxy-partner-0":
			linkOut = &cfg.Outbounds[i]
		case "proxy-partner-1":
			socksOut = &cfg.Outbounds[i]
		}
	}
	if linkOut == nil || socksOut == nil {
		t.Fatalf("lane outbounds missing: %v", tags)
	}
	for _, tag := range tags {
		if tag == "proxy-partner-2" {
			t.Fatal("an unreadable link produced an outbound")
		}
	}
	if linkOut.Protocol != "vless" || linkOut.StreamSettings == nil {
		t.Fatalf("link outbound: %+v", linkOut)
	}
	if socksOut.Protocol != "socks" || socksOut.StreamSettings != nil {
		t.Fatalf("socks outbound: %+v", socksOut)
	}
	// The config Xray reads must carry the link's REALITY material verbatim.
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"publicKey":"PK"`, `"shortId":"ab"`, `"flow":"xtls-rprx-vision"`, `"tag":"pool-partner"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("config lacks %s", want)
		}
	}
}

// A share link's port hopping is written in the form the Xray the config is for
// reads: the udp mask for one that has it, quicParams.udpHop for an older one, which
// refuses the whole config over the mask.
func TestLaneLinkHopFollowsXrayVersion(t *testing.T) {
	t.Parallel()
	rc := model.RoutingConfig{Lanes: []model.EgressLane{
		{ID: "partner", Name: "partner", Enabled: true, Domains: []string{"example.com"}},
	}}
	pool := map[string][]model.ProxyEndpoint{"partner": {
		{Protocol: "hysteria2", Address: "9.9.9.9", Port: 443, Link: "hysteria2://pw@9.9.9.9:443?sni=x&mport=20000-30000#h"},
	}}
	for legacy, want := range map[bool]string{
		false: `"type":"udphop"`,
		true:  `"udpHop":{"ports":"20000-30000"}`,
	} {
		cfg, err := Generate(laneSettings(rc), nil, Options{PanelDest: "127.0.0.1:8080", LegacyUDPHop: legacy}, pool)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(cfg)
		if !strings.Contains(string(raw), want) {
			t.Errorf("legacy=%v: config lacks %s", legacy, want)
		}
	}
}

func TestReadsUDPHopMask(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]bool{
		"26.9.30": true, "v26.9.30": true, "26.10.10": true, "27.1.1": true, "26.12.0": true,
		"26.9.9": false, "26.7.28": false, "25.12.31": false, "": false, "26.9": false, "dev": false,
	} {
		if got := ReadsUDPHopMask(v); got != want {
			t.Errorf("%q: %v, want %v", v, got, want)
		}
	}
}
