package xray

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func xhttpSettings() *model.Settings {
	set := baseSettings()
	set.XHTTPEnabled, set.XHTTPPath = true, "/abcdefghij"
	return set
}

// The lane listens on its inner address with no TLS of its own (the TCP-TLS lane's ended
// at the public port) and reads the PROXY line the route in front writes.
func TestXHTTPLaneInbound(t *testing.T) {
	users := []model.User{{ID: 1, UUID: "uuid-1"}, {ID: 2, UUID: "uuid-2"}}
	cfg, err := Generate(xhttpSettings(), users, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := findInbound(cfg, TagXHTTP)
	if in == nil {
		t.Fatal("xhttp-in missing")
	}
	listen, port := InnerListen(XHTTPInnerAddr)
	if in.Listen != listen || in.Port != port {
		t.Errorf("listens on %s:%d, want %s:%d", in.Listen, in.Port, listen, port)
	}
	st := in.StreamSettings
	if st.Network != "xhttp" || st.Security != "" || st.TLSSettings != nil || string(st.Sockopt) != string(acceptProxy) {
		t.Errorf("stream = %+v, want XHTTP without TLS, with the PROXY line", st)
	}
	if st.XHTTPSettings == nil || st.XHTTPSettings.Path != "/abcdefghij" {
		t.Errorf("xhttpSettings = %+v", st.XHTTPSettings)
	}
	if c := in.Settings.(VLESSInboundSettings).Clients; len(c) != 2 || c[0].Flow != "" {
		t.Errorf("clients = %+v", c)
	}

	path, addr, ok := XHTTPRoute(cfg)
	if !ok || path != "/abcdefghij" || addr != XHTTPInnerAddr {
		t.Errorf("route = %q %q %v", path, addr, ok)
	}
	raw, _ := json.Marshal(cfg)
	if p, a, ok := XHTTPRouteRaw(raw); !ok || p != path || a != addr {
		t.Errorf("raw route = %q %q %v", p, a, ok)
	}
	// Its client sockets are the front's: a removed user's are closed there.
	if _, ok := tcpUserPorts(raw)[TagXHTTP]; !ok {
		t.Error("the cut does not track the lane")
	}
	if !slices.Contains(EnabledInboundTags(xhttpSettings(), nil), TagXHTTP) {
		t.Error("live removal skips the lane")
	}
	stubbed := false
	for _, s := range UserInbounds(xhttpSettings(), nil, users, model.LocalNodeID, nil) {
		if s.Tag == TagXHTTP {
			stubbed = s.Listen == listen && s.Port == port
		}
	}
	if !stubbed {
		t.Error("live add does not reach the lane on its address")
	}
}

// Off, or with no path to route, there is no inbound and no route.
func TestXHTTPLaneOff(t *testing.T) {
	for _, set := range []*model.Settings{baseSettings(), func() *model.Settings {
		s := xhttpSettings()
		s.XHTTPPath = ""
		return s
	}()} {
		cfg, err := Generate(set, []model.User{{ID: 1, UUID: "u"}}, Options{PanelDest: "127.0.0.1:8080"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if findInbound(cfg, TagXHTTP) != nil {
			t.Error("xhttp-in generated for a lane that is off")
		}
		if _, _, ok := XHTTPRoute(cfg); ok {
			t.Error("a route for a lane that is off")
		}
	}
}

// Its bytes went through the TCP-TLS inbound's fallback: counted once, on their own.
func TestLanesTakeXHTTPOffVLESS(t *testing.T) {
	got := lanesOf(map[string]Traffic{TagVLESS: {Up: 100, Down: 1000}, TagXHTTP: {Up: 40, Down: 600}})
	if got[TagVLESS] != (Traffic{Up: 60, Down: 400}) || got[TagXHTTP] != (Traffic{Up: 40, Down: 600}) {
		t.Errorf("lanes = %+v", got)
	}
}
