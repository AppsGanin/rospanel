package xray

import (
	"encoding/json"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func baseSettings() *model.Settings {
	return &model.Settings{
		Host: "vpn.example.com", SNI: "vpn.example.com",
		CertPath: "/c.pem", KeyPath: "/k.pem",
		VLESSPort: 443, RealityPort: 8443, HysteriaPort: 60000,
		VLESSEnabled: true, HysteriaEnabled: true,
	}
}

func findInbound(cfg *Config, tag string) *Inbound {
	for i := range cfg.Inbounds {
		if cfg.Inbounds[i].Tag == tag {
			return &cfg.Inbounds[i]
		}
	}
	return nil
}

// :443 must carry exactly one fallback — the default one to the panel. The
// path-keyed fallback that used to dispatch a secret path to the loopback Trojan
// inbound was an oracle: every other request on that port answered like a website
// and that one did not.
func TestVisionHasOnlyTheDefaultFallback(t *testing.T) {
	cfg, err := Generate(baseSettings(), nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	in := findInbound(cfg, TagVLESS)
	if in == nil {
		t.Fatal("vless inbound missing")
	}
	s, ok := in.Settings.(VLESSInboundSettings)
	if !ok {
		t.Fatalf("unexpected settings type %T", in.Settings)
	}
	if len(s.Fallbacks) != 1 {
		t.Fatalf("expected exactly one fallback, got %d: %+v", len(s.Fallbacks), s.Fallbacks)
	}
	if s.Fallbacks[0].Path != "" {
		t.Errorf("the remaining fallback must not be path-keyed, got %q", s.Fallbacks[0].Path)
	}
	if findInbound(cfg, "trojan-in") != nil {
		t.Error("the loopback Trojan inbound must be gone")
	}
}

// The built-in REALITY lane runs on XHTTP, not gRPC: gRPC+REALITY is the most
// fingerprinted of the combinations, and with a REALITY config present XHTTP's
// default mode resolves to stream-one.
func TestRealityLaneUsesXHTTP(t *testing.T) {
	set := baseSettings()
	set.RealityEnabled = true
	set.RealityPrivateKey = "priv"
	set.RealityDest = "www.apple.com"
	set.RealityShortID = "aabb"
	set.RealityPath = "/secret"

	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	in := findInbound(cfg, TagReality)
	if in == nil {
		t.Fatal("reality inbound missing")
	}
	if in.StreamSettings.Network != "xhttp" {
		t.Errorf("network = %q, want xhttp", in.StreamSettings.Network)
	}
	if in.StreamSettings.GRPCSettings != nil {
		t.Error("grpcSettings must not be emitted for an XHTTP inbound")
	}
	if in.StreamSettings.XHTTPSettings == nil || in.StreamSettings.XHTTPSettings.Path != "/secret" {
		t.Errorf("xhttpSettings = %+v, want path /secret", in.StreamSettings.XHTTPSettings)
	}
}

// A custom inbound becomes a listener of its own, with the transport/security
// fields its combination actually uses — and nothing else, so a stray block can't
// make Xray reject the config.
func TestCustomInboundShape(t *testing.T) {
	users := []model.User{{ID: 1, UUID: "uuid-1", Password: "pw"}}

	ws := model.Inbound{
		ID: 5, Enabled: true, Name: "WS", Protocol: model.InbVLESS, Port: 9443,
		Opts: model.InboundOpts{
			Transport: model.TrWS, Security: model.SecTLS, Path: "/w", Host: "cdn.example.com",
		},
	}
	ws.Normalize()
	reality := model.Inbound{
		ID: 6, Enabled: true, Name: "R", Protocol: model.InbVLESS, Port: 9444,
		Opts: model.InboundOpts{
			Transport: model.TrXHTTP, Security: model.SecReality, Path: "/r",
			RealityDest: "www.apple.com", RealityPrivateKey: "priv", RealityShortID: "aa,bb",
		},
	}
	reality.Normalize()
	hy := model.Inbound{
		ID: 7, Enabled: true, Name: "H", Protocol: model.InbHysteria, Port: 7000,
		Opts: model.InboundOpts{HopStart: 7001, HopEnd: 7100},
	}
	hy.Normalize()

	cfg, err := Generate(baseSettings(), users, Options{
		PanelDest: "127.0.0.1:8080",
		Custom:    []model.Inbound{ws, reality, hy},
	}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := findInbound(cfg, ws.Tag())
	if got == nil {
		t.Fatal("custom ws inbound missing")
	}
	if got.Port != 9443 || got.Protocol != "vless" {
		t.Errorf("ws inbound = %s:%d, want vless:9443", got.Protocol, got.Port)
	}
	if got.StreamSettings.WSSettings == nil || got.StreamSettings.WSSettings.Path != "/w" {
		t.Errorf("wsSettings = %+v", got.StreamSettings.WSSettings)
	}
	if got.StreamSettings.WSSettings.Host != "cdn.example.com" {
		t.Errorf("ws host = %q", got.StreamSettings.WSSettings.Host)
	}
	// WebSocket completes its upgrade over HTTP/1.1; offering h2 here would let a
	// client negotiate a protocol the transport can't carry.
	if alpn := got.StreamSettings.TLSSettings.ALPN; len(alpn) != 1 || alpn[0] != "http/1.1" {
		t.Errorf("ws alpn = %v, want [http/1.1]", alpn)
	}
	vs, ok := got.Settings.(VLESSInboundSettings)
	if !ok || len(vs.Clients) != 1 || vs.Clients[0].ID != "uuid-1" {
		t.Fatalf("ws clients = %+v", got.Settings)
	}
	if vs.Clients[0].Flow != "" {
		t.Errorf("Vision must not be set on a WebSocket inbound, got %q", vs.Clients[0].Flow)
	}
	if vs.Clients[0].Email != model.UserEmail(1) {
		t.Errorf("client email = %q — per-user stats depend on it", vs.Clients[0].Email)
	}

	r := findInbound(cfg, reality.Tag())
	if r == nil || r.StreamSettings.Security != "reality" {
		t.Fatalf("reality inbound = %+v", r)
	}
	if r.StreamSettings.TLSSettings != nil {
		t.Error("a REALITY inbound must not also carry our own certificate")
	}
	if ids := r.StreamSettings.RealitySettings.ShortIds; len(ids) != 2 {
		t.Errorf("shortIds = %v, want both stored ids", ids)
	}
	if r.StreamSettings.RealitySettings.Dest != "www.apple.com:443" {
		t.Errorf("dest = %q", r.StreamSettings.RealitySettings.Dest)
	}

	h := findInbound(cfg, hy.Tag())
	if h == nil || h.Protocol != "hysteria" {
		t.Fatalf("hysteria inbound = %+v", h)
	}
	// QUIC needs ALPN h3 exactly, or the handshake dies with "no application protocol".
	if alpn := h.StreamSettings.TLSSettings.ALPN; len(alpn) != 1 || alpn[0] != "h3" {
		t.Errorf("hysteria alpn = %v, want [h3]", alpn)
	}
	hs, ok := h.Settings.(HysteriaInboundSettings)
	if !ok || hs.Version != 2 || len(hs.Users) != 1 || hs.Users[0].Auth != "pw" {
		t.Fatalf("hysteria settings = %+v", h.Settings)
	}

	// The whole document has to survive a round trip: Xray parses JSON, so a struct
	// that can't marshal is a config that never applies.
	if _, err := json.Marshal(cfg); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}

// The live add/remove-user API is driven by the inbound LIST, not by the settings
// booleans. Without this a user added while a custom inbound exists would reach it
// only after a full restart — and, worse, a user removed would keep working through
// it until then.
func TestLiveUserAPICoversCustomInbounds(t *testing.T) {
	set := baseSettings()
	users := []model.User{{ID: 1, UUID: "u", Password: "pw"}}
	custom := model.Inbound{
		ID: 5, Enabled: true, Name: "WS", Protocol: model.InbVLESS, Port: 9443,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/w"},
	}
	custom.Normalize()

	tags := EnabledInboundTags(set, []model.Inbound{custom})
	if !contains(tags, custom.Tag()) {
		t.Errorf("removal targets %v miss the custom inbound %q", tags, custom.Tag())
	}

	stubs := UserInbounds(set, []model.Inbound{custom}, users, model.LocalNodeID, nil)
	var found bool
	for _, s := range stubs {
		if s.Tag == custom.Tag() {
			found = true
			if s.Port != 9443 {
				t.Errorf("stub port = %d; `xray api adu` parses each entry as a full inbound", s.Port)
			}
		}
	}
	if !found {
		t.Errorf("add-user stubs %+v miss the custom inbound", stubs)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// The advanced blocks have to reach the generated config verbatim — that is the whole
// point of storing them raw — and land in the transport they belong to.
func TestAdvancedParamsReachTheConfig(t *testing.T) {
	extra := `{"noSSEHeader":true,"xmux":{"maxConcurrency":"8-32"}}`

	xh := model.Inbound{
		ID: 11, Enabled: true, Name: "XH", Protocol: model.InbVLESS, Port: 9443,
		Opts: model.InboundOpts{
			Transport: model.TrXHTTP, Security: model.SecTLS, Path: "/x",
			XHTTPExtra: json.RawMessage(extra),
			Sockopt:    json.RawMessage(`{"tcpCongestion":"bbr"}`),
			TLSExtra:   json.RawMessage(`{"maxVersion":"1.3","rejectUnknownSni":true}`),
		},
	}
	xh.Normalize()

	masq := model.Inbound{
		ID: 12, Enabled: true, Name: "M", Protocol: model.InbTrojan, Port: 9444,
		Opts: model.InboundOpts{
			Transport: model.TrTCP, Security: model.SecTLS,
			HeaderType: "http", HeaderHosts: []string{"cdn.example.com"},
			HeaderPaths: []string{"/assets/app.js"},
		},
	}
	masq.Normalize()

	grpc := model.Inbound{
		ID: 13, Enabled: true, Name: "G", Protocol: model.InbVLESS, Port: 9445,
		Opts: model.InboundOpts{
			Transport: model.TrGRPC, Security: model.SecTLS, ServiceName: "svc",
			Authority: "grpc.example.com", MultiMode: true,
		},
	}
	grpc.Normalize()

	cfg, err := Generate(baseSettings(), []model.User{{ID: 1, UUID: "u", Password: "p"}},
		Options{PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{xh, masq, grpc}}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	x := findInbound(cfg, xh.Tag())
	if x.StreamSettings.XHTTPSettings.Extra == nil {
		t.Fatal("xhttp extra missing from the config")
	}
	// Byte-for-byte: a re-encoded blob would be a chance to drift from what the
	// client is told in the link, which carries the same stored text.
	if got := string(x.StreamSettings.XHTTPSettings.Extra); got != extra {
		t.Errorf("extra was not passed through verbatim:\n got %s\nwant %s", got, extra)
	}
	if string(x.StreamSettings.Sockopt) != `{"tcpCongestion":"bbr"}` {
		t.Errorf("sockopt = %s", x.StreamSettings.Sockopt)
	}
	// The TLS extra keys are merged into the one tlsSettings object Xray reads, and
	// must not displace the fields the panel derives.
	tlsJSON, err := json.Marshal(x.StreamSettings.TLSSettings)
	if err != nil {
		t.Fatalf("marshal tls: %v", err)
	}
	var tls map[string]any
	if err := json.Unmarshal(tlsJSON, &tls); err != nil {
		t.Fatalf("tls not an object: %v", err)
	}
	if tls["maxVersion"] != "1.3" || tls["rejectUnknownSni"] != true {
		t.Errorf("tls extra keys missing: %s", tlsJSON)
	}
	if tls["serverName"] != "vpn.example.com" || tls["certificates"] == nil {
		t.Errorf("the panel's own tls fields were lost: %s", tlsJSON)
	}

	m := findInbound(cfg, masq.Tag())
	h := m.StreamSettings.TCPSettings.Header
	if h == nil || h.Type != "http" {
		t.Fatalf("tcp masquerade missing: %+v", m.StreamSettings.TCPSettings)
	}
	if got := h.Request.Headers["Host"]; len(got) != 1 || got[0] != "cdn.example.com" {
		t.Errorf("masquerade host = %v", got)
	}
	if got := h.Request.Path; len(got) != 1 || got[0] != "/assets/app.js" {
		t.Errorf("masquerade path = %v", got)
	}

	g := findInbound(cfg, grpc.Tag())
	if g.StreamSettings.GRPCSettings.Authority != "grpc.example.com" || !g.StreamSettings.GRPCSettings.MultiMode {
		t.Errorf("grpc extras = %+v", g.StreamSettings.GRPCSettings)
	}

	if _, err := json.Marshal(cfg); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}

// Groups are a server-side gate: a restricted user's credential must be absent from
// the lanes their groups don't grant, so a hand-crafted link can't reach them — the
// hidden lane isn't just missing from the subscription.
func TestAccessGatesClientLists(t *testing.T) {
	set := baseSettings()
	set.RealityEnabled, set.RealityPrivateKey, set.RealityDest, set.RealityShortID, set.RealityPath =
		true, "priv", "www.apple.com", "aa", "/s"
	users := []model.User{
		{ID: 1, UUID: "u1", Password: "p1"}, // unrestricted (no entry in the map)
		{ID: 2, UUID: "u2", Password: "p2"}, // restricted: only VLESS built-in + custom 5
	}
	custom := model.Inbound{
		ID: 5, Enabled: true, Name: "WS", Protocol: model.InbVLESS, Port: 9443,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/w"},
	}
	custom.Normalize()

	access := map[int64]model.Access{
		2: {Tokens: map[string]bool{
			model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true,
			model.InboundToken(5):                                 true,
		}},
	}
	cfg, err := Generate(set, users, Options{
		PanelDest: "127.0.0.1:8080", ServerID: model.LocalNodeID,
		Custom: []model.Inbound{custom}, Access: access,
	}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	ids := func(tag string) map[string]bool {
		in := findInbound(cfg, tag)
		out := map[string]bool{}
		switch s := in.Settings.(type) {
		case VLESSInboundSettings:
			for _, c := range s.Clients {
				out[c.Email] = true
			}
		case HysteriaInboundSettings:
			for _, c := range s.Users {
				out[c.Email] = true
			}
		}
		return out
	}
	u1, u2 := model.UserEmail(1), model.UserEmail(2)

	// VLESS-Vision: both (u2 is granted it).
	if v := ids(TagVLESS); !v[u1] || !v[u2] {
		t.Errorf("vless clients = %v, want both", v)
	}
	// REALITY: only the unrestricted u1 — u2's groups don't grant it.
	if r := ids(TagReality); !r[u1] || r[u2] {
		t.Errorf("reality clients = %v, want only u1", r)
	}
	// Hysteria2: only u1.
	if h := ids(TagHysteria); !h[u1] || h[u2] {
		t.Errorf("hysteria clients = %v, want only u1", h)
	}
	// Custom inbound 5: both (u2 is granted it).
	if c := ids(custom.Tag()); !c[u1] || !c[u2] {
		t.Errorf("custom clients = %v, want both", c)
	}

	// A different server id ⇒ u2's builtin:0:vless grant no longer applies, so on a
	// node they'd have no built-in VLESS. This is what makes grants per-server.
	cfgNode, _ := Generate(set, users, Options{
		PanelDest: "127.0.0.1:8080", ServerID: 7,
		Access: access,
	}, nil)
	if v := func() map[string]bool {
		out := map[string]bool{}
		if s, ok := findInbound(cfgNode, TagVLESS).Settings.(VLESSInboundSettings); ok {
			for _, c := range s.Clients {
				out[c.Email] = true
			}
		}
		return out
	}(); v[u2] {
		t.Errorf("on server 7 u2 should have no built-in VLESS (grant is for server 0): %v", v)
	}
}

// The LIVE add-user path (adu) must gate exactly like the full generator: a restricted
// user is added only to the inbounds their groups grant. Without this a user added to
// the running Xray between reconciles could land in a lane they aren't allowed.
func TestUserInboundsRespectsAccess(t *testing.T) {
	set := baseSettings()
	set.RealityEnabled = true
	users := []model.User{{ID: 2, UUID: "u2", Password: "p2"}}
	custom := model.Inbound{
		ID: 5, Enabled: true, Name: "WS", Protocol: model.InbVLESS, Port: 9443,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/w"},
	}
	custom.Normalize()
	access := map[int64]model.Access{
		2: {Tokens: map[string]bool{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true}},
	}
	stubs := UserInbounds(set, []model.Inbound{custom}, users, model.LocalNodeID, access)

	got := map[string]bool{}
	for _, s := range stubs {
		got[s.Tag] = true
	}
	if !got[TagVLESS] {
		t.Error("granted VLESS lane missing from the adu stubs")
	}
	if got[TagReality] {
		t.Error("REALITY lane must not be added — not granted")
	}
	if got[TagHysteria] {
		t.Error("Hysteria lane must not be added — not granted")
	}
	if got[custom.Tag()] {
		t.Error("custom inbound must not be added — not granted")
	}

	// An unrestricted user (nil access) still lands everywhere — the historical path.
	all := UserInbounds(set, []model.Inbound{custom}, users, model.LocalNodeID, nil)
	if len(all) < 3 {
		t.Errorf("unrestricted user should be added to every lane, got %d stubs", len(all))
	}
}
