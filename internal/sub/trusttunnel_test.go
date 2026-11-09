package sub

import (
	"html"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// TrustTunnel is on the page, with a button that opens the link in the app, and in no
// subscription: no client that reads one imports tt://.
func TestTrustTunnelOnThePageOnly(t *testing.T) {
	set := &model.Settings{
		Host: "vpn.example.com", SubPath: "sub", SNI: "vpn.example.com",
		ServerID: model.LocalNodeID, VLESSEnabled: true, VLESSPort: 443,
		TrustTunnelEnabled: true, SubShowConfigs: true,
	}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555", SubToken: "tok"}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}

	page, err := Page(u, set, servers, Billing{}, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatal(err)
	}
	tt := link.TrustTunnel(u, set)
	s := html.UnescapeString(string(page))
	if !strings.Contains(s, `href="`+tt+`"`) {
		t.Fatalf("no open-in-app button for %s", tt)
	}
	for _, l := range ShareLinksFor(u, servers, "") {
		if strings.HasPrefix(l, "tt://") {
			t.Fatalf("tt:// in a subscription: %s", l)
		}
	}

	// A user whose groups leave the lane out gets no link.
	limited := []Server{{Set: set, Access: model.Access{Tokens: map[string]bool{
		model.BuiltinToken(model.LocalNodeID, model.LaneVLESS): true,
	}}}}
	page, err = Page(u, set, limited, Billing{}, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "tt://") {
		t.Fatal("tt:// link for a user the lane is not granted to")
	}
}
