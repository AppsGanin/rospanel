package xray

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// mieru-in is there only while the lane is on, holds the users its access lets in, and
// joins the live user updates; internal/mieru reads its clients and address back.
func TestMieruInbound(t *testing.T) {
	set := baseSettings()
	if cfg, err := Generate(set, naiveUsers(), Options{PanelDest: "127.0.0.1:8080"}, nil); err != nil {
		t.Fatal(err)
	} else if findInbound(cfg, TagMieru) != nil {
		t.Fatal("mieru-in with the lane off")
	}

	set.MieruEnabled, set.MieruPort = true, 34567
	access := map[int64]model.Access{2: {Tokens: map[string]bool{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true}}}
	cfg, err := Generate(set, naiveUsers(), Options{PanelDest: "127.0.0.1:8080", Access: access}, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := findInbound(cfg, TagMieru)
	if in == nil {
		t.Fatal("no mieru-in")
	}
	listen, port := InnerListen(MieruInnerAddr)
	if in.Listen != listen || in.Port != port || string(in.StreamSettings.Sockopt) != `{"acceptProxyProtocol":true}` {
		t.Fatalf("mieru-in %+v", in)
	}
	clients, addr := MieruClients(cfg)
	if len(clients) != 1 || clients[0].Email != "u1" || addr != MieruInnerAddr {
		t.Fatalf("clients %+v at %q, want only u1 (u2's group has no mieru)", clients, addr)
	}
	raw, _ := json.Marshal(cfg)
	if c, a := MieruClientsRaw(raw); len(c) != 1 || a != MieruInnerAddr {
		t.Fatalf("raw: %+v %q", c, a)
	}
	if !slices.Contains(EnabledInboundTags(set, nil), TagMieru) {
		t.Fatal("mieru-in is not among the live-updated inbounds")
	}
	stubs := UserInbounds(set, nil, naiveUsers(), model.LocalNodeID, access)
	if i := slices.IndexFunc(stubs, func(s Inbound) bool { return s.Tag == TagMieru }); i < 0 ||
		len(stubs[i].Settings.(VLESSInboundSettings).Clients) != 1 {
		t.Fatalf("live stubs %+v", stubs)
	}
	if !laneTags[TagMieru] {
		t.Fatal("mieru-in is not a lane for the statistics")
	}
}

// The TCP ports a config's inbounds hold, read from the config or from its JSON: UDP
// inbounds and sockets (port 0) are not among them.
func TestTCPListenerPorts(t *testing.T) {
	set := baseSettings()
	set.MieruEnabled, set.MieruPort = true, 34567
	cfg, err := Generate(set, naiveUsers(), Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ports := TCPListenerPorts(cfg)
	raw, _ := json.Marshal(cfg)
	rawPorts := TCPListenerPortsRaw(raw)
	if ports[set.VLESSPort] != TagVLESS || rawPorts[set.VLESSPort] != TagVLESS {
		t.Fatalf("ports %v / %v, want vless-in on %d", ports, rawPorts, set.VLESSPort)
	}
	for p, tag := range ports {
		if tag == TagHysteria || p == 0 {
			t.Fatalf("%s on %d among the TCP listeners", tag, p)
		}
		if rawPorts[p] != tag {
			t.Fatalf("raw %v differs from %v", rawPorts, ports)
		}
	}
}
