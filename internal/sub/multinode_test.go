package sub

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func testSet(host string) *model.Settings {
	return &model.Settings{
		Host: host, SNI: host,
		VLESSPort: 443, RealityPort: 8443, HysteriaPort: 443,
		VLESSEnabled: true, HysteriaEnabled: true,
		RealityEnabled:   true,
		RealityPublicKey: "pub", RealityShortID: "aa", RealityPath: "/svc",
	}
}

// A single (local) server must produce byte-identical output through the Multi
// entrypoints and the legacy single-set ones, so enabling multi-node changes
// nothing for existing installs.
func TestSingleServerUnchanged(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	set := testSet("panel.example.com")
	one := One(set)

	if a, b := strings.Join(ShareLinks(u, one[0]), "\n"), strings.Join(ShareLinksAll(u, one), "\n"); a != b {
		t.Errorf("ShareLinks mismatch:\n legacy=%q\n multi =%q", a, b)
	}
	if ClashYAML(u, set) != ClashYAMLMulti(u, one) {
		t.Error("Clash single-server output differs between legacy and multi")
	}
	if SingBoxJSON(u, set) != SingBoxJSONMulti(u, one) {
		t.Error("sing-box single-server output differs between legacy and multi")
	}
}

// Two servers produce one entry per lane × server, each labelled with its node
// name, so a client can tell them apart.
func TestMultiNodeLinksLabelled(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	local := testSet("panel.example.com")
	node := testSet("nl1.example.com")
	node.NodeLabel = "Нидерланды"

	servers := []Server{{Set: local, Access: model.UnrestrictedAccess()}, {Set: node, Access: model.UnrestrictedAccess()}}
	links := ShareLinksAll(u, servers)
	if len(links) != 6 { // 3 built-in lanes × 2 servers
		t.Fatalf("expected 6 links, got %d", len(links))
	}
	joined := strings.Join(links, "\n")
	if !strings.Contains(joined, "nl1.example.com") || !strings.Contains(joined, "panel.example.com") {
		t.Fatalf("links missing a server host:\n%s", joined)
	}
	// The node's entries carry a "· <name>" label suffix; in the URL fragment the
	// middle dot is percent-encoded as %C2%B7 — its presence proves the node label
	// was appended (the local server's entries have no such suffix).
	if !strings.Contains(joined, "%C2%B7") {
		t.Fatalf("node label suffix missing from links:\n%s", joined)
	}

	// Clash proxy names are unique across the two servers.
	yaml := ClashYAMLMulti(u, servers)
	if !strings.Contains(yaml, `type: vless, server: "nl1.example.com"`) {
		t.Fatalf("node vless proxy missing from clash:\n%s", yaml)
	}
}

// A custom inbound is per-server: it must appear in that server's links only, with
// its own name, and must not leak into a server that doesn't define it.
func TestCustomInboundsArePerServer(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	local := testSet("panel.example.com")
	node := testSet("nl1.example.com")
	node.NodeLabel = "Нидерланды"

	ws := model.Inbound{
		ID: 7, ServerID: 3, Enabled: true, Name: "WS резерв",
		Protocol: model.InbVLESS, Port: 8080,
		Opts: model.InboundOpts{
			Transport: model.TrWS, Security: model.SecTLS, Path: "/w",
		},
	}
	servers := []Server{{Set: local, Access: model.UnrestrictedAccess()}, {Set: node, Custom: []model.Inbound{ws}, Access: model.UnrestrictedAccess()}}

	links := ShareLinksAll(u, servers)
	if len(links) != 7 { // 3 built-in × 2 servers + 1 custom on the node
		t.Fatalf("expected 7 links, got %d:\n%s", len(links), strings.Join(links, "\n"))
	}
	var custom []string
	for _, l := range links {
		if strings.Contains(l, ":8080?") {
			custom = append(custom, l)
		}
	}
	if len(custom) != 1 {
		t.Fatalf("custom inbound should appear once, got %d", len(custom))
	}
	if !strings.Contains(custom[0], "nl1.example.com") {
		t.Fatalf("custom inbound attached to the wrong server: %s", custom[0])
	}
	if !strings.Contains(custom[0], "type=ws") || !strings.Contains(custom[0], "path=%2Fw") {
		t.Fatalf("custom inbound link missing its transport params: %s", custom[0])
	}
}

// XHTTP has no sing-box transport at all, so an XHTTP inbound must be dropped from
// the sing-box profile while still reaching Xray-core clients through the link list.
// Emitting an approximation instead would risk the client rejecting the whole file.
func TestXHTTPSkippedInSingBoxKeptInLinks(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	set := testSet("panel.example.com")
	set.VLESSEnabled, set.RealityEnabled, set.HysteriaEnabled = false, false, false

	xh := model.Inbound{
		ID: 4, Enabled: true, Name: "XH", Protocol: model.InbVLESS, Port: 9443,
		Opts: model.InboundOpts{Transport: model.TrXHTTP, Security: model.SecTLS, Path: "/x"},
	}
	servers := []Server{{Set: set, Custom: []model.Inbound{xh}, Access: model.UnrestrictedAccess()}}

	if links := ShareLinksAll(u, servers); len(links) != 1 || !strings.Contains(links[0], "type=xhttp") {
		t.Fatalf("xhttp link missing from the universal list: %v", links)
	}
	if js := SingBoxJSONMulti(u, servers); strings.Contains(js, `"tag": "XH"`) {
		t.Fatalf("xhttp must not appear in a sing-box profile:\n%s", js)
	}
	// mihomo does have xhttp for VLESS, so the same inbound belongs in the Clash file.
	if yaml := ClashYAMLMulti(u, servers); !strings.Contains(yaml, "xhttp-opts") {
		t.Fatalf("xhttp missing from clash output:\n%s", yaml)
	}
}

// The advanced XHTTP block only earns its keep if the CLIENT gets the same object —
// otherwise the two ends disagree and nothing connects. It rides the link's extra=
// parameter as the identical text the inbound was configured with.
func TestXHTTPExtraReachesTheLink(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	set := testSet("panel.example.com")
	set.VLESSEnabled, set.RealityEnabled, set.HysteriaEnabled = false, false, false

	extra := `{"noSSEHeader":true,"xmux":{"maxConcurrency":"8-32"}}`
	in := model.Inbound{
		ID: 4, Enabled: true, Name: "XH", Protocol: model.InbVLESS, Port: 9443,
		Opts: model.InboundOpts{
			Transport: model.TrXHTTP, Security: model.SecTLS, Path: "/x",
			XHTTPExtra: json.RawMessage(extra),
		},
	}
	in.Normalize()

	links := ShareLinksAll(u, []Server{{Set: set, Custom: []model.Inbound{in}, Access: model.UnrestrictedAccess()}})
	if len(links) != 1 {
		t.Fatalf("expected one link, got %v", links)
	}
	parsed, err := url.Parse(links[0])
	if err != nil {
		t.Fatalf("link does not parse: %v", err)
	}
	if got := parsed.Query().Get("extra"); got != extra {
		t.Errorf("extra in link:\n got %s\nwant %s", got, extra)
	}
}

// The raw-TCP HTTP masquerade is the other setting both ends must agree on: the
// server only recognizes a connection that opens with the same framing.
func TestTCPMasqueradeReachesTheLink(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	set := testSet("panel.example.com")
	set.VLESSEnabled, set.RealityEnabled, set.HysteriaEnabled = false, false, false

	in := model.Inbound{
		ID: 5, Enabled: true, Name: "M", Protocol: model.InbVLESS, Port: 9444,
		Opts: model.InboundOpts{
			Transport: model.TrTCP, Security: model.SecTLS,
			HeaderType: "http", HeaderHosts: []string{"a.example.com", "b.example.com"},
			HeaderPaths: []string{"assets/app.js"},
		},
	}
	in.Normalize()

	links := ShareLinksAll(u, []Server{{Set: set, Custom: []model.Inbound{in}, Access: model.UnrestrictedAccess()}})
	parsed, err := url.Parse(links[0])
	if err != nil {
		t.Fatalf("link does not parse: %v", err)
	}
	q := parsed.Query()
	if q.Get("headerType") != "http" {
		t.Errorf("headerType = %q", q.Get("headerType"))
	}
	if q.Get("host") != "a.example.com,b.example.com" {
		t.Errorf("host = %q", q.Get("host"))
	}
	if q.Get("path") != "/assets/app.js" {
		t.Errorf("path = %q", q.Get("path"))
	}
}

// A restricted user's subscription lists only the lanes their groups grant — the same
// gate the config applies, so the links shown match the credentials actually present.
func TestSubscriptionRespectsAccess(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	set := testSet("panel.example.com")
	set.ServerID = model.LocalNodeID
	ws := model.Inbound{
		ID: 9, Enabled: true, Name: "WS", Protocol: model.InbVLESS, Port: 9443,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/w"},
	}
	// Granted: built-in VLESS only. Not the custom inbound, not REALITY, not Hysteria2.
	access := model.Access{Tokens: map[string]bool{
		model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true,
	}}
	servers := []Server{{Set: set, Custom: []model.Inbound{ws}, Access: access}}

	links := ShareLinksAll(u, servers)
	if len(links) != 1 {
		t.Fatalf("expected only the granted VLESS link, got %d: %v", len(links), links)
	}
	if !strings.Contains(links[0], "type=tcp") || !strings.Contains(links[0], "flow=xtls-rprx-vision") {
		t.Errorf("the one link should be built-in VLESS-Vision: %s", links[0])
	}
	// The custom inbound must not appear despite being enabled — access wins.
	for _, l := range links {
		if strings.Contains(l, ":9443?") {
			t.Errorf("custom inbound leaked into a restricted user's links: %s", l)
		}
	}
}
