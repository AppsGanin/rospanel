package xray

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func naiveUsers() []model.User {
	return []model.User{
		{ID: 1, UUID: "11111111-1111-1111-1111-111111111111", Password: "p1"},
		{ID: 2, UUID: "22222222-2222-2222-2222-222222222222", Password: "p2"},
	}
}

// naive-in is the shape internal/naive dials and the e2e test runs: its inner address,
// no TLS, PROXY header accepted, UUID without flow.
func TestNaiveInboundShape(t *testing.T) {
	set := baseSettings()
	set.NaiveEnabled = true
	cfg, err := Generate(set, naiveUsers(), Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := findInbound(cfg, TagNaive)
	if in == nil {
		t.Fatal("no naive-in")
	}
	got, _ := json.Marshal(in)
	listen, port := InnerListen(NaiveInnerAddr)
	want := fmt.Sprintf(`{"tag":"naive-in","listen":%q,"port":%d,"protocol":"vless",`, listen, port) +
		`"settings":{"clients":[{"id":"11111111-1111-1111-1111-111111111111","email":"u1"},` +
		`{"id":"22222222-2222-2222-2222-222222222222","email":"u2"}],"decryption":"none"},` +
		`"streamSettings":{"network":"tcp","sockopt":{"acceptProxyProtocol":true}},` +
		`"sniffing":{"enabled":true,"destOverride":["http","tls","quic"]}}`
	if string(got) != want {
		t.Fatalf("naive-in\n got %s\nwant %s", got, want)
	}
	if c, addr := NaiveClients(cfg); len(c) != 2 || c[1].Email != "u2" || addr != NaiveInnerAddr {
		t.Fatalf("NaiveClients = %+v %q", c, addr)
	}
	raw, _ := json.Marshal(cfg)
	if c, addr := NaiveClientsRaw(raw); len(c) != 2 || c[0].ID != naiveUsers()[0].UUID || addr != NaiveInnerAddr {
		t.Fatalf("NaiveClientsRaw = %+v %q", c, addr)
	}
}

// Off, there is no naive-in: a loopback port nobody uses is one more thing that could
// keep Xray from starting.
func TestNaiveOffHasNoInbound(t *testing.T) {
	cfg, err := Generate(baseSettings(), naiveUsers(), Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if findInbound(cfg, TagNaive) != nil {
		t.Fatal("naive-in generated while the lane is off")
	}
	if c, addr := NaiveClients(cfg); c != nil || addr != "" {
		t.Fatalf("NaiveClients while off: %+v %q", c, addr)
	}
	if slices.Contains(EnabledInboundTags(baseSettings(), nil), TagNaive) {
		t.Fatal("off lane listed for live removal")
	}
}

// A group that does not grant the lane keeps the user out of naive-in, and the live
// add path agrees with the generator.
func TestNaiveFollowsAccess(t *testing.T) {
	set := baseSettings()
	set.NaiveEnabled = true
	access := map[int64]model.Access{1: {Tokens: map[string]bool{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true}}}
	cfg, err := Generate(set, naiveUsers(), Options{PanelDest: "127.0.0.1:8080", ServerID: model.LocalNodeID, Access: access}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := NaiveClients(cfg)
	if len(c) != 1 || c[0].Email != "u2" {
		t.Fatalf("naive-in clients %+v, want only u2", c)
	}
	var live []VLESSClient
	for _, in := range UserInbounds(set, nil, naiveUsers(), model.LocalNodeID, access) {
		if in.Tag == TagNaive {
			live = in.Settings.(VLESSInboundSettings).Clients
		}
	}
	if len(live) != 1 || live[0].Email != "u2" {
		t.Fatalf("live add %+v, want only u2", live)
	}
	if !slices.Contains(EnabledInboundTags(set, nil), TagNaive) {
		t.Fatal("naive-in not listed for live removal")
	}
}

// A TCP listener already on naive-in's port — an inbound made before the port was
// reserved — moves naive-in to the next free one instead of keeping Xray from starting;
// a UDP one shares the number without a clash.
func TestNaiveStepsPastATakenPort(t *testing.T) {
	set := baseSettings()
	set.NaiveEnabled = true
	listen, port := InnerListen(NaiveInnerAddr)
	held := port
	if held == 0 {
		held = 18444
	}
	tcp := model.Inbound{ID: 5, Enabled: true, Name: "old", Protocol: model.InbVLESS, Port: held,
		Opts: model.InboundOpts{Transport: model.TrTCP, Security: model.SecNone}}
	tcp.Normalize()
	cfg, err := Generate(set, naiveUsers(), Options{PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{tcp}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if port == 0 { // a socket: nothing to step past
		if in := findInbound(cfg, TagNaive); in == nil || in.Listen != listen || in.Port != 0 {
			t.Fatalf("naive-in %+v, want it on %s", in, listen)
		}
		return
	}
	if in := findInbound(cfg, TagNaive); in == nil || in.Port != port+1 {
		t.Fatalf("naive-in %+v, want it on %d", in, port+1)
	}
	if _, addr := NaiveClients(cfg); addr != fmt.Sprintf("127.0.0.1:%d", port+1) {
		t.Fatalf("naive-in address %q", addr)
	}

	udp := model.Inbound{ID: 6, Enabled: true, Name: "hy", Protocol: model.InbHysteria, Port: port}
	udp.Normalize()
	cfg, err = Generate(set, naiveUsers(), Options{PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{udp}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in := findInbound(cfg, TagNaive); in == nil || in.Port != port {
		t.Fatalf("a UDP listener moved naive-in: %+v", in)
	}
}

// NaiveProxy rides the VLESS-TCP-TLS port's fallback, not the VLESS lane: with VLESS
// switched off the listener and its fallback stay, holding no VLESS user.
func TestNaiveWithVLESSOff(t *testing.T) {
	set := baseSettings()
	set.VLESSEnabled, set.NaiveEnabled = false, true
	cfg, err := Generate(set, naiveUsers(), Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := findInbound(cfg, TagVLESS)
	if v == nil {
		t.Fatal("no listener on the VLESS-TCP-TLS port")
	}
	st := v.Settings.(VLESSInboundSettings)
	if len(st.Clients) != 0 || len(st.Fallbacks) != 1 {
		t.Fatalf("vless-in with VLESS off: %d clients, fallbacks %+v", len(st.Clients), st.Fallbacks)
	}
	if c, _ := NaiveClients(cfg); len(c) != 2 {
		t.Fatalf("naive-in clients %+v", c)
	}
}
