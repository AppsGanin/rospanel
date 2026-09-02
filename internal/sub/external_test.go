package sub

import (
	"strings"
	"testing"

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
