package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

type xrayCfg struct {
	Remarks   string           `json:"remarks"`
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []struct {
		Tag            string         `json:"tag"`
		Protocol       string         `json:"protocol"`
		Settings       map[string]any `json:"settings"`
		StreamSettings map[string]any `json:"streamSettings"`
	} `json:"outbounds"`
}

func parseXray(t *testing.T, body string) []xrayCfg {
	t.Helper()
	var out []xrayCfg
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("not a JSON array of configs: %v\n%s", err, body)
	}
	return out
}

// Every lane the link list carries becomes one complete config with the same
// credentials and parameters.
func TestXrayJSONMirrorsTheShareLinks(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "6bbd16cd-dfc1-47c4-9426-59b57b92b173", Password: "pw"}
	set := testSet("panel.example.com")
	set.TLSPinSHA256 = "ab12"
	ws := model.Inbound{
		ID: 7, Enabled: true, Name: "WS", Protocol: model.InbTrojan, Port: 8080,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/w"},
	}
	servers := []Server{{Set: set, Custom: []model.Inbound{ws}, Access: model.UnrestrictedAccess()}}
	cfgs := parseXray(t, XrayJSONMulti(u, servers, model.DefaultSubDPI()))
	if len(cfgs) != 4 { // VLESS-Vision, XHTTP-REALITY, Hysteria2, Trojan-WS
		t.Fatalf("want 4 configs, got %d", len(cfgs))
	}
	byProto := map[string]xrayCfg{}
	for _, c := range cfgs {
		if len(c.Inbounds) != 2 || c.Remarks == "" {
			t.Errorf("config %q: inbounds=%d", c.Remarks, len(c.Inbounds))
		}
		byProto[c.Outbounds[0].Protocol+"/"+c.Outbounds[0].StreamSettings["security"].(string)] = c
	}
	vision := byProto["vless/tls"]
	vnext := vision.Outbounds[0].Settings["vnext"].([]any)[0].(map[string]any)
	user := vnext["users"].([]any)[0].(map[string]any)
	if vnext["address"] != "panel.example.com" || vnext["port"] != float64(443) ||
		user["id"] != u.UUID || user["flow"] != "xtls-rprx-vision" {
		t.Errorf("vision outbound: %+v", vnext)
	}
	tls := vision.Outbounds[0].StreamSettings["tlsSettings"].(map[string]any)
	if tls["serverName"] != "panel.example.com" || tls["pinnedPeerCertSha256"] != "ab12" || tls["allowInsecure"] != false {
		t.Errorf("tls settings: %+v", tls)
	}
	reality := byProto["vless/reality"]
	rs := reality.Outbounds[0].StreamSettings["realitySettings"].(map[string]any)
	if rs["publicKey"] != "pub" || rs["shortId"] != "aa" || reality.Outbounds[0].StreamSettings["network"] != "xhttp" {
		t.Errorf("reality: %+v", reality.Outbounds[0].StreamSettings)
	}
	trojan := byProto["trojan/tls"]
	srv := trojan.Outbounds[0].Settings["servers"].([]any)[0].(map[string]any)
	wss := trojan.Outbounds[0].StreamSettings["wsSettings"].(map[string]any)
	if srv["password"] != "pw" || srv["port"] != float64(8080) || wss["path"] != "/w" {
		t.Errorf("trojan-ws: %+v %+v", srv, wss)
	}
	// Hysteria2: the version has to appear in BOTH blocks, the auth in the stream
	// one, and the address/port in the settings block rather than a server list.
	hy := byProto["hysteria/tls"]
	hs := hy.Outbounds[0].Settings
	stream := hy.Outbounds[0].StreamSettings
	hset := stream["hysteriaSettings"].(map[string]any)
	htls := stream["tlsSettings"].(map[string]any)
	if hs["version"] != float64(2) || hs["address"] != "panel.example.com" || hs["port"] != float64(443) {
		t.Errorf("hysteria settings: %+v", hs)
	}
	if hset["version"] != float64(2) || hset["auth"] != "pw" || stream["network"] != "hysteria" {
		t.Errorf("hysteria stream: %+v", stream)
	}
	if alpn, _ := htls["alpn"].([]any); len(alpn) != 1 || alpn[0] != "h3" || htls["pinnedPeerCertSha256"] != "ab12" {
		t.Errorf("hysteria tls: %+v", htls)
	}
	if _, hopping := stream["finalmask"]; hopping {
		t.Errorf("no hop range configured, yet finalmask present: %+v", stream)
	}
	// With everything off there is no fragment outbound and no dialerProxy anywhere.
	for _, c := range cfgs {
		for _, o := range c.Outbounds {
			if o.Tag == "fragment" {
				t.Errorf("%s: fragment outbound present with DPI off", c.Remarks)
			}
			if sock, ok := o.StreamSettings["sockopt"]; ok {
				t.Errorf("%s: unexpected sockopt %v", c.Remarks, sock)
			}
		}
	}
}

// Fragment chains a freedom outbound in front of TLS lanes only; noise goes in
// front of every lane. Both settings land verbatim.
func TestXrayJSONFragmentAndNoise(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "6bbd16cd-dfc1-47c4-9426-59b57b92b173", Password: "pw"}
	set := testSet("panel.example.com")
	dpi := model.DefaultSubDPI()
	dpi.Fragment = true
	dpi.FragmentLength = "50-100"
	cfgs := parseXray(t, XrayJSONMulti(u, One(set), dpi))
	for _, c := range cfgs {
		security := c.Outbounds[0].StreamSettings["security"]
		var shaper map[string]any
		for _, o := range c.Outbounds {
			if o.Tag == "fragment" {
				shaper = o.Settings
			}
		}
		sock, _ := c.Outbounds[0].StreamSettings["sockopt"].(map[string]any)
		if c.Outbounds[0].StreamSettings["network"] == "hysteria" {
			// A QUIC lane never dials through the TCP freedom outbound; chaining one
			// would be dead weight at best.
			if shaper != nil || sock != nil {
				t.Errorf("%s: hysteria must not be chained through the shaper", c.Remarks)
			}
			continue
		}
		switch security {
		case "tls":
			if shaper == nil || sock["dialerProxy"] != "fragment" {
				t.Errorf("%s: TLS lane should dial through the fragment outbound", c.Remarks)
			}
			frag, _ := shaper["fragment"].(map[string]any)
			if frag["packets"] != "tlshello" || frag["length"] != "50-100" || frag["interval"] != "10-20" {
				t.Errorf("fragment settings: %v", shaper)
			}
			if _, noisy := shaper["noises"]; noisy {
				t.Errorf("noise is off, yet noises present: %v", shaper)
			}
		default:
			if shaper != nil || sock != nil {
				t.Errorf("%s (%v): non-TLS lane must not be fragmented", c.Remarks, security)
			}
		}
	}

	dpi.Fragment = false
	dpi.Noise = true
	dpi.NoiseType = "str"
	dpi.NoisePacket = "hello"
	cfgs = parseXray(t, XrayJSONMulti(u, One(set), dpi))
	for _, c := range cfgs {
		var shaper map[string]any
		for _, o := range c.Outbounds {
			if o.Tag == "fragment" {
				shaper = o.Settings
			}
		}
		sock, _ := c.Outbounds[0].StreamSettings["sockopt"].(map[string]any)
		if c.Outbounds[0].StreamSettings["network"] == "hysteria" {
			if shaper != nil || sock != nil {
				t.Errorf("%s: hysteria must not be chained through the shaper", c.Remarks)
			}
			continue
		}
		if shaper == nil || sock["dialerProxy"] != "fragment" {
			t.Errorf("%s: noise should apply to every TCP lane", c.Remarks)
			continue
		}
		noises, _ := shaper["noises"].([]any)
		if len(noises) != 1 || noises[0].(map[string]any)["type"] != "str" || noises[0].(map[string]any)["packet"] != "hello" {
			t.Errorf("noise settings: %v", shaper)
		}
		if _, f := shaper["fragment"]; f {
			t.Errorf("fragment is off, yet present: %v", shaper)
		}
	}
}

// A client without geoip.dat must still load the config, so the private ranges are
// literal CIDRs rather than the geoip:private shorthand.
func TestXrayJSONRoutingNeedsNoGeoFiles(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "6bbd16cd-dfc1-47c4-9426-59b57b92b173", Password: "pw"}
	body := XrayJSONMulti(u, One(testSet("h")), model.DefaultSubDPI())
	if strings.Contains(body, "geoip:") || strings.Contains(body, "geosite:") {
		t.Errorf("the config depends on geo files:\n%s", body)
	}
	var cfgs []struct {
		Routing struct {
			Rules []struct {
				IP          []string `json:"ip"`
				OutboundTag string   `json:"outboundTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal([]byte(body), &cfgs); err != nil {
		t.Fatal(err)
	}
	r := cfgs[0].Routing.Rules
	if len(r) != 1 || r[0].OutboundTag != "direct" || len(r[0].IP) < 8 {
		t.Fatalf("routing rules: %+v", r)
	}
	want := map[string]bool{"127.0.0.0/8": false, "10.0.0.0/8": false, "192.168.0.0/16": false, "::1/128": false}
	for _, ip := range r[0].IP {
		if _, ok := want[ip]; ok {
			want[ip] = true
		}
	}
	for ip, seen := range want {
		if !seen {
			t.Errorf("private range %s missing", ip)
		}
	}
}

// Port hopping rides in the link's fm parameter and lands under finalmask, which
// is where Xray now reads it (hysteriaSettings only warns).
func TestXrayJSONHysteriaPortHopping(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "6bbd16cd-dfc1-47c4-9426-59b57b92b173", Password: "pw"}
	set := testSet("panel.example.com")
	set.VLESSEnabled, set.RealityEnabled = false, false
	set.HopStart, set.HopEnd, set.HopInterval = 20000, 20100, "5-10"
	cfgs := parseXray(t, XrayJSONMulti(u, One(set), model.DefaultSubDPI()))
	if len(cfgs) != 1 {
		t.Fatalf("want the hysteria lane alone, got %d", len(cfgs))
	}
	stream := cfgs[0].Outbounds[0].StreamSettings
	mask, ok := stream["finalmask"].(map[string]any)
	if !ok {
		t.Fatalf("no finalmask on a hopping lane: %+v", stream)
	}
	quic, _ := mask["quicParams"].(map[string]any)
	hop, _ := quic["udpHop"].(map[string]any)
	if hop["ports"] != "20000-20100" || hop["interval"] != "5-10" || quic["congestion"] != "bbr" {
		t.Errorf("hop params: %+v", quic)
	}
}

func TestXrayJSONSkipsWhatItCannotCarry(t *testing.T) {
	if _, ok := xrayConfigFromLink("vless://id@h:notaport?type=tcp#x", model.DefaultSubDPI()); ok {
		t.Error("bad port accepted")
	}
	if _, ok := xrayConfigFromLink("wireguard://key@h:443#x", model.DefaultSubDPI()); ok {
		t.Error("an unknown scheme was accepted")
	}
	// A Shadowsocks 2022 link: method, then everything after the first colon.
	u := model.User{ID: 1, UUID: "6bbd16cd-dfc1-47c4-9426-59b57b92b173", Password: "pw"}
	set := testSet("h")
	set.VLESSEnabled, set.RealityEnabled, set.HysteriaEnabled = false, false, false
	ss := model.Inbound{
		ID: 9, Enabled: true, Name: "SS", Protocol: model.InbShadowsocks, Port: 8388,
		Opts: model.InboundOpts{Method: "2022-blake3-aes-128-gcm", ShadowKey: "c2VydmVya2V5"},
	}
	body := XrayJSONMulti(u, []Server{{Set: set, Custom: []model.Inbound{ss}, Access: model.UnrestrictedAccess()}}, model.DefaultSubDPI())
	cfgs := parseXray(t, body)
	if len(cfgs) != 1 || cfgs[0].Outbounds[0].Protocol != "shadowsocks" {
		t.Fatalf("shadowsocks config: %s", body)
	}
	srv := cfgs[0].Outbounds[0].Settings["servers"].([]any)[0].(map[string]any)
	if srv["method"] != "2022-blake3-aes-128-gcm" || !strings.HasPrefix(srv["password"].(string), "c2VydmVya2V5:") {
		t.Errorf("ss server: %v", srv)
	}
}
