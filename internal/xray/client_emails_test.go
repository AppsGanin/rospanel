package xray

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// ClientEmails is what a node's reports are checked against, so a user it misses has
// their traffic on that node thrown away. It is compared here with the users read
// straight out of the JSON the node is sent, on a config with every lane, every custom
// protocol the panel offers, access groups narrowing some of them, and the inbounds
// that carry no panel users at all.
func TestClientEmailsMatchTheConfigSent(t *testing.T) {
	set := baseSettings()
	set.RealityEnabled, set.RealityPrivateKey, set.RealityDest, set.RealityShortID, set.RealityPath =
		true, "priv", "www.apple.com", "aa", "/s"
	set.ProxySocksEnabled, set.ProxySocksPort = true, 1080
	set.ProxyHTTPEnabled, set.ProxyHTTPPort = true, 3128
	set.ProxyAccounts = []model.SystemProxyAccount{{User: "u9", Pass: "not-a-panel-user"}}
	users := []model.User{
		{ID: 1, UUID: "uuid-1", Password: "pw1"},
		{ID: 2, UUID: "uuid-2", Password: "pw2"},
		{ID: 3, UUID: "uuid-3", Password: "pw3"},
	}
	var custom []model.Inbound
	for i, protocol := range model.InboundProtocols {
		in := model.Inbound{ID: int64(10 + i), Enabled: true, Name: protocol, Protocol: protocol, Port: 9000 + i}
		switch protocol {
		case model.InbVLESS, model.InbTrojan:
			in.Opts = model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/p"}
		case model.InbShadowsocks:
			in.Opts = model.InboundOpts{Method: model.SS2022AES128, ShadowKey: "AAAAAAAAAAAAAAAAAAAAAA=="}
		}
		in.Normalize()
		custom = append(custom, in)
	}
	// An inbound nobody may use: Shadowsocks then carries its locked entry, which is
	// not a user and must not be counted as one.
	lockedSS := model.Inbound{ID: 30, Enabled: true, Name: "locked", Protocol: model.InbShadowsocks, Port: 9100,
		Opts: model.InboundOpts{Method: model.SS2022AES128, ShadowKey: "BBBBBBBBBBBBBBBBBBBBBB=="}}
	lockedSS.Normalize()
	custom = append(custom, lockedSS)
	access := map[int64]model.Access{
		// User 1 on every lane and every custom inbound but the locked one.
		1: {Tokens: map[string]bool{
			model.BuiltinToken(model.LocalNodeID, model.LaneVLESS):    true,
			model.BuiltinToken(model.LocalNodeID, model.LaneReality):  true,
			model.BuiltinToken(model.LocalNodeID, model.LaneHysteria): true,
			model.InboundToken(10): true, model.InboundToken(11): true,
			model.InboundToken(12): true, model.InboundToken(13): true,
		}},
		// User 2 only on the built-in VLESS lane and the custom Trojan inbound.
		2: {Tokens: map[string]bool{
			model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true,
			model.InboundToken(11):                                 true,
		}},
		// User 3 on nothing but the custom inbounds 10 and 12.
		3: {Tokens: map[string]bool{model.InboundToken(10): true, model.InboundToken(12): true}},
	}
	cfg, err := Generate(set, users, Options{
		PanelDest: "127.0.0.1:8080", ServerID: model.LocalNodeID, Custom: custom, Access: access,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cfg.ClientEmails()
	if !ok {
		t.Fatal("the generated config could not be read")
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Inbounds []struct {
			Protocol string                     `json:"protocol"`
			Settings map[string]json.RawMessage `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var want []string
	protocols := map[string]bool{}
	for _, in := range doc.Inbounds {
		protocols[in.Protocol] = true
		for _, key := range []string{"clients", "users"} {
			var list []struct {
				Email string `json:"email"`
			}
			if b, present := in.Settings[key]; present {
				if err := json.Unmarshal(b, &list); err != nil {
					t.Fatalf("%s %s: %v", in.Protocol, key, err)
				}
			}
			for _, u := range list {
				want = append(want, u.Email)
			}
		}
	}
	for _, p := range []string{"vless", "trojan", "hysteria", "shadowsocks", "socks", "http"} {
		if !protocols[p] {
			t.Fatalf("the fixture has no %s inbound, so it proves nothing about one", p)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("ClientEmails = %v\nthe config sent = %v", got, want)
	}
	// And the fixture really narrows: user 1 is on all seven, users 2 and 3 on two each,
	// and the inbound nobody may use holds only its locked entry.
	count := map[string]int{}
	for _, e := range got {
		count[e]++
	}
	if count["u1"] != 7 || count["u2"] != 2 || count["u3"] != 2 || count["ss-locked-"+lockedSS.Tag()] != 1 {
		t.Errorf("per-user placements = %v, the access groups did not narrow as meant", count)
	}
}

// Settings of a type it does not know, on an inbound that carries users, is a config it
// cannot read — not a config with nobody in it.
func TestClientEmailsRefusesUnknownSettings(t *testing.T) {
	cfg := &Config{Inbounds: []Inbound{
		{Tag: "api", Protocol: "dokodemo-door", Settings: DokodemoSettings{Address: "127.0.0.1"}},
		{Tag: "x", Protocol: "vless", Settings: &VLESSInboundSettings{Clients: []VLESSClient{{Email: "u1"}}}},
	}}
	if _, ok := cfg.ClientEmails(); ok {
		t.Error("a pointer to the settings was read as an inbound with no users")
	}
	cfg.Inbounds[1].Settings = VLESSInboundSettings{Clients: []VLESSClient{{Email: "u1"}}}
	if got, ok := cfg.ClientEmails(); !ok || !slices.Equal(got, []string{"u1"}) {
		t.Errorf("ClientEmails = %v, %v", got, ok)
	}
}
