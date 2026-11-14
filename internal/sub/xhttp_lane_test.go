package sub

import (
	"net/url"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func xhttpSet() *model.Settings {
	set := testSet("vpn.example.com")
	set.VLESSPort = 8443
	set.XHTTPEnabled, set.XHTTPPath = true, "/abcdefghij"
	return set
}

func xhttpLink(links []string) string {
	for _, l := range links {
		if strings.Contains(l, "type=xhttp") && strings.Contains(l, "abcdefghij") {
			return l
		}
	}
	return ""
}

// The link reaches the TCP-TLS port on the server's own name, over TLS, on the path.
func TestXHTTPLaneLink(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "11111111-2222-3333-4444-555555555555", Password: "pw"}
	got := xhttpLink(ShareLinks(u, Server{Set: xhttpSet(), Access: model.UnrestrictedAccess()}))
	if got == "" {
		t.Fatal("no XHTTP link")
	}
	p, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if p.Scheme != "vless" || p.User.Username() != u.UUID || p.Host != "vpn.example.com:8443" || p.Fragment != model.ProtoXHTTP {
		t.Errorf("link = %s", got)
	}
	q := p.Query()
	for k, want := range map[string]string{
		"type": "xhttp", "security": "tls", "sni": "vpn.example.com", "path": "/abcdefghij",
		"host": "vpn.example.com", "encryption": "none", "alpn": "h2,http/1.1",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	// A self-signed certificate is pinned, as on VLESS-TCP-TLS.
	set := xhttpSet()
	set.TLSPinSHA256 = "abc123"
	if l := xhttpLink(ShareLinks(u, Server{Set: set, Access: model.UnrestrictedAccess()})); !strings.Contains(l, "pcs=abc123") {
		t.Errorf("no pin: %s", l)
	}
	// A custom name, variables and all.
	set = xhttpSet()
	set.XHTTPName = "XH {user}"
	if l := xhttpLink(ShareLinks(u, Server{Set: set, Access: model.UnrestrictedAccess()})); !strings.HasSuffix(l, "#XH%20u") {
		t.Errorf("label: %s", l)
	}
}

func TestXHTTPLaneClash(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	servers := []Server{{Set: xhttpSet(), Access: model.UnrestrictedAccess()}}
	yaml := ClashYAMLFor(u, servers, "mihomo/1.19.22")
	var line string
	for _, l := range strings.Split(yaml, "\n") {
		if strings.Contains(l, "network: xhttp") && strings.Contains(l, "abcdefghij") {
			line = l
		}
	}
	// A core known to lack XHTTP would dial it as plain TCP: an entry that never connects.
	for _, ua := range []string{"mihomo/1.19.21", "clash-verge/v2.2.3"} {
		if strings.Contains(ClashYAMLFor(u, servers, ua), "abcdefghij") {
			t.Errorf("%s got the lane", ua)
		}
	}
	// One the panel cannot place keeps it.
	if !strings.Contains(ClashYAMLFor(u, servers, "Stash/3.3.1"), "abcdefghij") {
		t.Error("an unknown client lost the lane")
	}
	for _, want := range []string{`name: "VLESS-XHTTP-TLS"`, `server: "vpn.example.com"`, "port: 8443", "tls: true", `servername: "vpn.example.com"`, "/abcdefghij"} {
		if !strings.Contains(line, want) {
			t.Errorf("clash proxy lacks %q:\n%s", want, line)
		}
	}
}

// Access gates it like any built-in lane, sing-box has no form for it, and a rule can
// hide it.
func TestXHTTPLaneGates(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	access := model.Access{Tokens: map[string]bool{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true}}
	if l := xhttpLink(ShareLinks(u, Server{Set: xhttpSet(), Access: access})); l != "" {
		t.Errorf("handed out without access: %s", l)
	}
	if strings.Contains(SingBoxJSONMulti(u, []Server{{Set: xhttpSet(), Access: model.UnrestrictedAccess()}}), "abcdefghij") {
		t.Error("sing-box got the lane")
	}
	if Protocols(model.SubActionSingbox, "", nil)[model.LaneXHTTP] || !Protocols(model.SubActionClash, "mihomo/1.19.22", nil)[model.LaneXHTTP] ||
		Protocols(model.SubActionClash, "mihomo/1.19.21", nil)[model.LaneXHTTP] {
		t.Error("the rule preview is wrong about the lane")
	}
	hidden := Server{Set: xhttpSet(), Access: model.UnrestrictedAccess(), Hidden: map[string]bool{model.LaneXHTTP: true}}
	if l := xhttpLink(ShareLinks(u, hidden)); l != "" {
		t.Errorf("a rule could not hide it: %s", l)
	}
}
