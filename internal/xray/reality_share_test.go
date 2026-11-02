package xray

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// realityOn443 is a server with a domain whose REALITY lane is put on the TCP-TLS
// lane's port and masks as a donor — the shape the editor allows.
func realityOn443() *model.Settings {
	set := baseSettings()
	set.RealityPort = set.VLESSPort
	set.RealityEnabled = true
	set.RealityDest = "max.ru"
	set.RealityPrivateKey = "priv"
	set.RealityShortID = "ab12"
	set.RealityPath = "/x"
	return set
}

// The lane behind the front: inner address, PROXY header, the same one fallback-free
// reality settings — and the front told which names are its.
func TestRealityOnTheSharedPort(t *testing.T) {
	listen, port := InnerListen(RealityInnerAddr)
	cfg, err := Generate(realityOn443(), nil, Options{PanelDest: "127.0.0.1:8080", FrontVLESS: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := findInbound(cfg, TagReality)
	if in == nil {
		t.Fatal("reality inbound missing")
	}
	if in.Listen != listen || in.Port != port {
		t.Fatalf("lane on %s:%d, want %s:%d", in.Listen, in.Port, listen, port)
	}
	if string(in.StreamSettings.Sockopt) != `{"acceptProxyProtocol":true}` {
		t.Fatalf("sockopt %s, want the PROXY header accepted", in.StreamSettings.Sockopt)
	}
	if got := in.StreamSettings.RealitySettings.Dest; got != "max.ru:443" {
		t.Fatalf("dest %q, want the donor", got)
	}
	snis, behind := RealityRoute(cfg)
	if !behind || !slices.Equal(snis, []string{"max.ru"}) {
		t.Fatalf("route %v behind=%v, want max.ru behind the front", snis, behind)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rn, rbehind := RealityRouteRaw(raw); !rbehind || !slices.Equal(rn, []string{"max.ru"}) {
		t.Fatalf("raw route %v behind=%v, want the same", rn, rbehind)
	}
}

// A port of its own is nobody's business but Xray's: the front routes nothing.
func TestRealityOnItsOwnPort(t *testing.T) {
	set := realityOn443()
	set.RealityPort = 8443
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080", FrontVLESS: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in := findInbound(cfg, TagReality); in.Listen != "0.0.0.0" || in.Port != 8443 || in.StreamSettings.Sockopt != nil {
		t.Fatalf("lane on %s:%d sockopt %s, want the public port with none", in.Listen, in.Port, in.StreamSettings.Sockopt)
	}
	if snis, behind := RealityRoute(cfg); behind || snis != nil {
		t.Fatalf("route %v behind=%v, want no split", snis, behind)
	}
}

// The server's own name is the decoy's on that port, so a config asking REALITY to
// mask as it would hand every client to the decoy: it must not come up.
func TestRealityOnTheSharedPortRefusesItsOwnName(t *testing.T) {
	set := realityOn443()
	set.RealityDest = "max.ru,vpn.example.com"
	if _, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080", FrontVLESS: true}, nil); err == nil {
		t.Fatal("a shared REALITY masking as the server's own name was generated")
	}
}
