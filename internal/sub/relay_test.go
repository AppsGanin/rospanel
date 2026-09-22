package sub

import (
	"net/url"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func relayServer(lane string, granted bool) Server {
	access := model.Access{Tokens: map[string]bool{
		model.BuiltinToken(0, model.LaneVLESS):   true,
		model.BuiltinToken(0, model.LaneReality): true,
	}}
	if granted {
		access.Tokens[model.ExtToken(5)] = true
	}
	ext := model.ExtServer{ID: 5, Name: "Partner NL", Protocol: "vless", Host: "9.9.9.9", Port: 443,
		Link: "vless://secret@9.9.9.9:443?security=tls#Partner NL", Enabled: true, RelayLane: lane}
	return Server{Set: testSet("panel.example.com"), Access: access, External: []model.ExtServer{ext}, Relays: []model.ExtServer{ext}}
}

var relayUser = model.User{ID: 1, Name: "u", UUID: "6bbd16cd-dfc1-47c4-9426-59b57b92b173", Password: "pw"}

// A relayed server reaches the user as an entry of our lane under its own name, with
// the route in their UUID — and its own link, the partner's credential, never.
func TestRelayedServerIsOurLane(t *testing.T) {
	for _, lane := range []string{model.LaneVLESS, model.LaneReality} {
		srv := relayServer(lane, true)
		links := strings.Join(ShareLinks(relayUser, srv), "\n")
		if strings.Contains(links, "secret@") {
			t.Fatalf("%s: the partner's link was handed out:\n%s", lane, links)
		}
		want := "vless://6bbd16cd-dfc1-0005-9426-59b57b92b173@panel.example.com:"
		var relayed string
		for _, l := range ShareLinks(relayUser, srv) {
			if strings.HasPrefix(l, want) {
				relayed = l
			}
		}
		if relayed == "" {
			t.Fatalf("%s: no relayed entry in\n%s", lane, links)
		}
		u, _ := url.Parse(relayed)
		if u.Fragment != "Partner NL" {
			t.Errorf("%s: relayed entry named %q", lane, u.Fragment)
		}
		if (lane == model.LaneReality) != (u.Query().Get("security") == "reality") {
			t.Errorf("%s: relayed through the wrong lane: %s", lane, relayed)
		}
		clash := ClashYAMLMulti(relayUser, []Server{srv})
		if !strings.Contains(clash, `name: "Partner NL"`) || !strings.Contains(clash, "6bbd16cd-dfc1-0005-9426-59b57b92b173") || strings.Contains(clash, "secret") {
			t.Errorf("%s: clash profile:\n%s", lane, clash)
		}
		sb := SingBoxJSONMulti(relayUser, []Server{srv})
		if got := strings.Contains(sb, "6bbd16cd-dfc1-0005-9426-59b57b92b173"); got != (lane == model.LaneVLESS) {
			t.Errorf("%s: sing-box has the relayed entry = %v (sing-box has no XHTTP for REALITY)", lane, got)
		}
		if strings.Contains(sb, "secret") {
			t.Errorf("%s: sing-box carries the partner's credential", lane)
		}
	}
}

// Not granted, a lane that is not the user's, a UUID that already carries the route:
// no entry. A server handed out directly is unaffected.
func TestRelayedServerNeedsAllOfIt(t *testing.T) {
	if got := relayServer(model.LaneVLESS, false).relayEntries(relayUser); len(got) != 0 {
		t.Errorf("not granted, got %v", got)
	}
	srv := relayServer(model.LaneVLESS, true)
	delete(srv.Access.Tokens, model.BuiltinToken(0, model.LaneVLESS))
	if got := srv.relayEntries(relayUser); len(got) != 0 {
		t.Errorf("off the lane, got %v", got)
	}
	own := relayUser
	own.UUID = "aaaaaaaa-bbbb-0005-8ccc-dddddddddddd"
	if got := relayServer(model.LaneVLESS, true).relayEntries(own); len(got) != 0 {
		t.Errorf("a UUID carrying the route, got %v", got)
	}
	direct := relayServer("", true)
	direct.Relays = nil
	if links := strings.Join(ShareLinks(relayUser, direct), "\n"); !strings.Contains(links, "secret@") {
		t.Errorf("a direct server is no longer handed out:\n%s", links)
	}
}
