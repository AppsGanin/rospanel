package xray

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
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
			model.InboundToken(5): true,
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

// A Shadowsocks-2022 custom inbound: the server key and method sit at the top of
// settings, each admitted user carries their own derived key, UDP is relayed, and —
// unlike every other custom inbound — there is no streamSettings, because SS-2022 is
// raw TCP with its own AEAD and no TLS layer to configure.
func TestCustomShadowsocksInbound(t *testing.T) {
	users := []model.User{
		{ID: 1, UUID: "uuid-1", Password: "pw1"},
		{ID: 2, UUID: "uuid-2", Password: "pw2"},
	}
	ss := model.Inbound{
		ID: 9, Enabled: true, Name: "SS", Protocol: model.InbShadowsocks, Port: 9500,
		Opts: model.InboundOpts{
			Method:    model.SS2022AES128,
			ShadowKey: "AAAAAAAAAAAAAAAAAAAAAA==", // 16 bytes base64, the right length
		},
	}
	ss.Normalize()

	cfg, err := Generate(baseSettings(), users, Options{
		PanelDest: "127.0.0.1:8080",
		Custom:    []model.Inbound{ss},
	}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := findInbound(cfg, ss.Tag())
	if got == nil {
		t.Fatal("custom shadowsocks inbound missing")
	}
	if got.Protocol != "shadowsocks" || got.Port != 9500 {
		t.Errorf("inbound = %s:%d, want shadowsocks:9500", got.Protocol, got.Port)
	}
	if got.StreamSettings != nil {
		t.Errorf("shadowsocks must carry no streamSettings, got %+v", got.StreamSettings)
	}
	s, ok := got.Settings.(ShadowsocksInboundSettings)
	if !ok {
		t.Fatalf("settings type %T, want ShadowsocksInboundSettings", got.Settings)
	}
	if s.Method != model.SS2022AES128 {
		t.Errorf("method = %q", s.Method)
	}
	if s.Password != ss.Opts.ShadowKey {
		t.Errorf("server key = %q, want the inbound's stored key", s.Password)
	}
	if s.Network != "tcp,udp" {
		t.Errorf("network = %q, want tcp,udp — UDP would be dropped otherwise", s.Network)
	}
	if len(s.Users) != 2 {
		t.Fatalf("users = %d, want 2", len(s.Users))
	}
	// Each user's key is derived from their UUID, is the method's length, and is not
	// the shared server key.
	for i, u := range users {
		c := s.Users[i]
		if c.Email != model.UserEmail(u.ID) {
			t.Errorf("client %d email = %q — per-user stats need it", i, c.Email)
		}
		want := model.UserShadowKey(u.UUID, model.SS2022AES128)
		if c.Password != want {
			t.Errorf("client %d key = %q, want the UUID-derived %q", i, c.Password, want)
		}
		if c.Password == s.Password {
			t.Errorf("client %d key equals the server key — they must differ", i)
		}
		if raw, err := base64.StdEncoding.DecodeString(c.Password); err != nil || len(raw) != 16 {
			t.Errorf("client %d key is not 16 base64 bytes: %v", i, err)
		}
	}
	// The two users get different keys.
	if s.Users[0].Password == s.Users[1].Password {
		t.Error("two users share a Shadowsocks key")
	}
}

// A Shadowsocks inbound that no user may use must NOT become an open door.
//
// Xray builds an SS-2022 inbound with an empty users list as a SINGLE-user server
// whose access key is the top-level server key — and that key is in every client
// link the inbound ever handed out. So revoking everyone (an emptied access group,
// every user deleted) would flip "nobody" into "anybody who kept a link", with no
// account and no quota. The generator must keep the users list non-empty with a key
// no client knows, so Xray stays multi-user and authenticates no one.
func TestShadowsocksEmptyUserListFailsClosed(t *testing.T) {
	ss := model.Inbound{
		ID: 3, Enabled: true, Name: "SS", Protocol: model.InbShadowsocks, Port: 9600,
		Opts: model.InboundOpts{
			Method: model.SS2022AES128, ShadowKey: base64.StdEncoding.EncodeToString(make([]byte, 16)),
		},
	}
	ss.Normalize()

	// No users at all — the worst case, and the one an operator reaches by revoking.
	cfg, err := Generate(baseSettings(), nil, Options{
		PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{ss},
	}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := findInbound(cfg, ss.Tag())
	if got == nil {
		t.Fatal("inbound missing")
	}
	s := got.Settings.(ShadowsocksInboundSettings)
	// The list stays non-empty, so Xray builds a multi-user server, not the single-
	// user one keyed by the server key.
	if len(s.Users) != 1 {
		t.Fatalf("empty-access SS inbound has %d users, want exactly 1 locked placeholder", len(s.Users))
	}
	locked := s.Users[0]
	// The placeholder key must NOT be the server key (that is the whole exposure), and
	// must not be derivable as any user's key either.
	if locked.Password == s.Password {
		t.Error("placeholder key equals the server key — the door is still open")
	}
	if locked.Password != model.LockedShadowKey(ss.Opts.ShadowKey, model.SS2022AES128) {
		t.Errorf("placeholder key = %q, not the locked derivation", locked.Password)
	}
	if raw, err := base64.StdEncoding.DecodeString(locked.Password); err != nil || len(raw) != 16 {
		t.Errorf("placeholder key is not 16 base64 bytes: %v", err)
	}
	// Stable across regenerations, so an all-revoked inbound doesn't churn the config
	// and restart Xray on every apply.
	cfg2, _ := Generate(baseSettings(), nil, Options{
		PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{ss},
	}, nil)
	s2 := findInbound(cfg2, ss.Tag()).Settings.(ShadowsocksInboundSettings)
	if s2.Users[0].Password != locked.Password {
		t.Error("placeholder key is not stable between generations — would thrash reloads")
	}

	// And the moment a real user is allowed, the placeholder is gone (not left behind
	// as an extra credential).
	cfg3, _ := Generate(baseSettings(), []model.User{{ID: 1, UUID: "uuid-1"}}, Options{
		PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{ss},
	}, nil)
	s3 := findInbound(cfg3, ss.Tag()).Settings.(ShadowsocksInboundSettings)
	if len(s3.Users) != 1 || s3.Users[0].Email != model.UserEmail(1) {
		t.Errorf("with one real user the list should be just that user, got %+v", s3.Users)
	}
}

// Shadowsocks-2022 must be covered by BOTH live-user paths, symmetrically. It
// implements AddUser/RemoveUser (unlike Hysteria2), so `adu` and `rmu` both work on
// it — and covering only one is worse than covering neither: it was listed for `rmu`
// (revoked users dropped live) but missing from `adu` (newly allowed users never
// added live), so a user's access lagged a full restart in exactly one direction.
func TestLiveUserAPICoversShadowsocks(t *testing.T) {
	set := baseSettings()
	users := []model.User{{ID: 1, UUID: "uuid-1"}}
	ss := model.Inbound{
		ID: 8, Enabled: true, Name: "SS", Protocol: model.InbShadowsocks, Port: 9700,
		Opts: model.InboundOpts{
			Method: model.SS2022AES128, ShadowKey: base64.StdEncoding.EncodeToString(make([]byte, 16)),
		},
	}
	ss.Normalize()

	// rmu side: the tag must be a removal target.
	if !contains(EnabledInboundTags(set, []model.Inbound{ss}), ss.Tag()) {
		t.Error("Shadowsocks inbound is missing from the rmu targets")
	}

	// adu side: a full, parseable stub with the method/key/network Xray needs to build
	// it, plus the user actually being added.
	stubs := UserInbounds(set, []model.Inbound{ss}, users, model.LocalNodeID, nil)
	var stub *Inbound
	for i := range stubs {
		if stubs[i].Tag == ss.Tag() {
			stub = &stubs[i]
		}
	}
	if stub == nil {
		t.Fatalf("Shadowsocks inbound missing from the adu stubs %+v", stubs)
	}
	s, ok := stub.Settings.(ShadowsocksInboundSettings)
	if !ok {
		t.Fatalf("stub settings type %T", stub.Settings)
	}
	if stub.Port != 9700 || s.Method != model.SS2022AES128 || s.Password == "" {
		t.Errorf("adu stub can't be parsed as a full SS inbound: port=%d method=%q key?%v",
			stub.Port, s.Method, s.Password != "")
	}
	if len(s.Users) != 1 || s.Users[0].Email != model.UserEmail(1) {
		t.Errorf("adu stub users = %+v, want the one allowed user", s.Users)
	}
	if s.Users[0].Password != model.UserShadowKey("uuid-1", model.SS2022AES128) {
		t.Error("adu stub carries the wrong per-user key")
	}
}
