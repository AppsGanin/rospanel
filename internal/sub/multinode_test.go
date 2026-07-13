package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func testSet(host string) *model.Settings {
	return &model.Settings{
		Host: host, SNI: host, WSPath: "/ws",
		VLESSPort: 443, RealityPort: 8443, HysteriaPort: 443,
		VLESSEnabled: true, TrojanEnabled: true, HysteriaEnabled: true,
		RealityEnabled:     true,
		RealityPublicKey:   "pub", RealityShortID: "aa", RealityServiceName: "svc",
	}
}

// A single (local) server must produce byte-identical output through the Multi
// entrypoints and the legacy single-set ones, so enabling multi-node changes
// nothing for existing installs.
func TestSingleServerUnchanged(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	set := testSet("panel.example.com")
	one := []*model.Settings{set}

	if a, b := strings.Join(ShareLinks(u, set), "\n"), strings.Join(ShareLinksAll(u, one), "\n"); a != b {
		t.Errorf("ShareLinks mismatch:\n legacy=%q\n multi =%q", a, b)
	}
	if ClashYAML(u, set) != ClashYAMLMulti(u, one) {
		t.Error("Clash single-server output differs between legacy and multi")
	}
	if SingBoxJSON(u, set) != SingBoxJSONMulti(u, one) {
		t.Error("sing-box single-server output differs between legacy and multi")
	}
}

// Two servers produce one entry per protocol × server, each labelled with its
// node name, so a client can tell them apart.
func TestMultiNodeLinksLabelled(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	local := testSet("panel.example.com")
	node := testSet("nl1.example.com")
	node.NodeLabel = "Нидерланды"

	links := ShareLinksAll(u, []*model.Settings{local, node})
	if len(links) != 8 { // 4 protocols × 2 servers
		t.Fatalf("expected 8 links, got %d", len(links))
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
	yaml := ClashYAMLMulti(u, []*model.Settings{local, node})
	if !strings.Contains(yaml, `type: vless, server: "nl1.example.com"`) {
		t.Fatalf("node vless proxy missing from clash:\n%s", yaml)
	}
}
