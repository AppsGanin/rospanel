package xray

import (
	"encoding/json"
	"testing"
)

// Behind the front the lane listens on loopback and reads the front's PROXY header;
// everything else about it — the one fallback, TLS — stays as it was.
func TestFrontMovesTheLaneToLoopback(t *testing.T) {
	cfg, err := Generate(baseSettings(), nil, Options{PanelDest: "127.0.0.1:8080", FrontVLESS: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := findInbound(cfg, TagVLESS)
	if in.Listen != "127.0.0.1" || in.Port != VLESSInnerPort {
		t.Fatalf("lane on %s:%d, want 127.0.0.1:%d", in.Listen, in.Port, VLESSInnerPort)
	}
	if string(in.StreamSettings.Sockopt) != `{"acceptProxyProtocol":true}` {
		t.Fatalf("sockopt %s, want the PROXY header accepted", in.StreamSettings.Sockopt)
	}
	if s := in.Settings.(VLESSInboundSettings); len(s.Fallbacks) != 1 || s.Fallbacks[0].Xver != 1 {
		t.Fatalf("fallbacks %+v, want the one PROXY-carrying fallback to the panel", s.Fallbacks)
	}

	plain, err := Generate(baseSettings(), nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in := findInbound(plain, TagVLESS); in.Listen != "0.0.0.0" || in.Port != 443 || in.StreamSettings.Sockopt != nil {
		t.Fatalf("without the front the lane is %s:%d sockopt %s, want 0.0.0.0:443 with none", in.Listen, in.Port, in.StreamSettings.Sockopt)
	}
}

// A node's finished config gets the same move, and says which port the front takes.
func TestFrontVLESSRaw(t *testing.T) {
	plain, err := Generate(baseSettings(), nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	moved, public := FrontVLESSRaw(raw)
	if public != 443 {
		t.Fatalf("public port %d, want 443", public)
	}
	var got Config
	if err := json.Unmarshal(moved, &got); err != nil {
		t.Fatal(err)
	}
	in := findInbound(&got, TagVLESS)
	if in.Listen != "127.0.0.1" || in.Port != VLESSInnerPort {
		t.Fatalf("lane on %s:%d, want 127.0.0.1:%d", in.Listen, in.Port, VLESSInnerPort)
	}
	var sockopt map[string]any
	if err := json.Unmarshal(in.StreamSettings.Sockopt, &sockopt); err != nil || sockopt["acceptProxyProtocol"] != true {
		t.Fatalf("sockopt %s, want acceptProxyProtocol", in.StreamSettings.Sockopt)
	}

	// A custom inbound from before the port was reserved keeps the lane where it is.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["inbounds"] = append(doc["inbounds"].([]any), map[string]any{"tag": "custom-7", "port": VLESSInnerPort, "protocol": "vless"})
	taken, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if out, public := FrontVLESSRaw(taken); public != 0 || string(out) != string(taken) {
		t.Fatalf("with the inner port taken: public %d, config changed %v; want 0 and unchanged", public, string(out) != string(taken))
	}
}
