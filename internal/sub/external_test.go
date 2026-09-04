package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// A subscription built from a server with no lanes of its own and two external
// servers: what reaches the user is exactly the external servers their access
// allows, in every format, and nothing when the server entry is not the master's.
func TestExternalServersFollowAccess(t *testing.T) {
	set := &model.Settings{Host: "1.2.3.4", SNI: "1.2.3.4", ServerID: model.LocalNodeID}
	ext := []model.ExtServer{
		{ID: 11, Name: "Partner NL", Protocol: "vless", Host: "9.9.9.9", Port: 443, Enabled: true,
			Link: "vless://uuid@9.9.9.9:443?type=tcp&security=tls&sni=nl.example&fp=chrome#Partner%20NL"},
		{ID: 12, Name: "Partner DE", Protocol: "hysteria2", Host: "8.8.8.8", Port: 443, Enabled: true,
			Link: "hysteria2://pw@8.8.8.8:443?sni=de.example#Partner%20DE"},
		{ID: 13, Name: "Off", Protocol: "trojan", Host: "7.7.7.7", Port: 443, Enabled: false,
			Link: "trojan://pw@7.7.7.7:443?security=tls&sni=x#Off"},
	}
	u := model.User{ID: 1, UUID: "u", Password: "p"}

	all := Server{Set: set, Access: model.UnrestrictedAccess(), External: ext}
	links := ShareLinks(u, all)
	if len(links) != 2 || links[0] != ext[0].Link || links[1] != ext[1].Link {
		t.Fatalf("unrestricted links: %v", links)
	}
	if yaml := ClashYAMLMulti(u, []Server{all}); !strings.Contains(yaml, `"Partner NL"`) || !strings.Contains(yaml, `"Partner DE"`) || strings.Contains(yaml, `"Off"`) {
		t.Fatalf("clash: %s", yaml)
	}
	if sb := SingBoxJSONMulti(u, []Server{all}); !strings.Contains(sb, `"Partner NL"`) || !strings.Contains(sb, `"hysteria2"`) {
		t.Fatalf("sing-box: %s", sb)
	}
	if xj := XrayJSONMulti(u, []Server{all}, model.SubDPI{}); !strings.Contains(xj, `"remarks": "Partner NL"`) || !strings.Contains(xj, `"protocol": "hysteria"`) {
		t.Fatalf("xray json: %s", xj)
	}

	// A group that grants one of them.
	one := all
	one.Access = model.Access{Tokens: map[string]bool{model.ExtToken(12): true}}
	if links = ShareLinks(u, one); len(links) != 1 || links[0] != ext[1].Link {
		t.Fatalf("restricted links: %v", links)
	}
	none := all
	none.Access = model.Access{Tokens: map[string]bool{}}
	if links = ShareLinks(u, none); len(links) != 0 {
		t.Fatalf("no grants: %v", links)
	}
}

// A REALITY lane is only handed out once the panel has minted its keys: the public
// key is what a client authenticates the handshake with, and a link without one
// fails with nothing a user could act on. A node added before its keys landed used
// to produce exactly that link.
func TestRealityLaneNeedsItsPublicKey(t *testing.T) {
	set := &model.Settings{
		Host: "1.2.3.4", SNI: "1.2.3.4", ServerID: model.LocalNodeID,
		RealityEnabled: true, RealityPort: 8443, RealityDest: "max.ru",
		RealityShortID: "ab", RealityPath: "/x",
	}
	u := model.User{ID: 1, UUID: "uuid", Password: "pw"}
	srv := Server{Set: set, Access: model.UnrestrictedAccess()}

	if links := ShareLinks(u, srv); len(links) != 0 {
		t.Fatalf("a keyless REALITY lane was handed out: %v", links)
	}
	if yaml := ClashYAMLMulti(u, []Server{srv}); strings.Contains(yaml, "reality-opts") {
		t.Error("a keyless REALITY proxy reached the Clash profile")
	}

	set.RealityPublicKey = "PUBKEY"
	links := ShareLinks(u, srv)
	if len(links) != 1 || !strings.Contains(links[0], "pbk=PUBKEY") {
		t.Fatalf("with a key the lane must be handed out: %v", links)
	}
}

// The subscription page is the fifth format, and it was the one that left the
// external servers out: they reached the link list, Clash, sing-box and the Xray
// JSON, but a user who opened the page saw only the operator's own servers. They
// come last there too, as they do in the link list.
func TestPageListsExternalServers(t *testing.T) {
	local := &model.Settings{
		Host: "vpn.example.com", SubPath: "sub", SNI: "vpn.example.com",
		ServerID: model.LocalNodeID, VLESSEnabled: true, VLESSPort: 443,
		SubShowConfigs: true,
	}
	ext := []model.ExtServer{
		{ID: 11, Name: "Partner NL", Protocol: "vless", Host: "9.9.9.9", Port: 443, Enabled: true,
			Link: "vless://uuid@9.9.9.9:443?type=tcp&security=tls&sni=nl.example#Partner%20NL"},
		{ID: 12, Name: "Off", Protocol: "trojan", Host: "7.7.7.7", Port: 443, Enabled: false,
			Link: "trojan://pw@7.7.7.7:443?security=tls&sni=x#Off"},
	}
	u := model.User{ID: 1, UUID: "u", Password: "p", SubToken: "tok"}
	servers := []Server{{Set: local, Access: model.UnrestrictedAccess(), External: ext}}

	html, err := Page(u, local, servers, Billing{}, Devices{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	if !strings.Contains(s, "Partner NL") || !strings.Contains(s, "9.9.9.9") {
		t.Errorf("the enabled external server is missing from the page")
	}
	if strings.Contains(s, "7.7.7.7") {
		t.Errorf("a disabled external server reached the page")
	}
	// Last: after the server's own lanes, the way the link list has them.
	if own, partner := strings.Index(s, "vpn.example.com:443"), strings.Index(s, "9.9.9.9"); own < 0 || partner < own {
		t.Errorf("external server is not last: own lane at %d, external at %d", own, partner)
	}
}

// Everything on the page that addresses the panel itself — the sub URL behind the
// QR and the copy button, the AmneziaWG download — must address the panel, whatever
// the ordering did to the server list. A node can come first by weight, distance or
// load, and a full master with hide-when-full leaves the list altogether; reading
// the first entry then pointed the page's own links at a node, which serves none of
// them.
func TestPageAddressesThePanelNotWhicheverServerIsFirst(t *testing.T) {
	local := &model.Settings{
		Host: "vpn.example.com", SubPath: "sub", ServerID: model.LocalNodeID,
		PanelName: "Panel", SubShowConfigs: true,
	}
	// The master is gone (full, hidden) and only a node is left.
	node := &model.Settings{
		Host: "node.example.com", SubPath: "sub", ServerID: 7, NodeLabel: "NL",
		VLESSEnabled: true, VLESSPort: 443, SNI: "node.example.com",
		AWGEnabled: true, AWGPort: 51820,
	}
	u := model.User{ID: 1, UUID: "u", Password: "p", SubToken: "tok"}

	html, err := Page(u, local, []Server{{Set: node, Access: model.UnrestrictedAccess()}}, Billing{}, Devices{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	if !strings.Contains(s, "https://vpn.example.com/sub/tok") {
		t.Errorf("the page does not carry the panel's own subscription URL")
	}
	if strings.Contains(s, "https://node.example.com/sub/") {
		t.Errorf("the page addressed the node for something the panel serves")
	}
	// The node's own lane still points at the node — only the panel's links moved.
	if !strings.Contains(s, "node.example.com:443") {
		t.Errorf("the node's own lane is missing from the page")
	}
}
