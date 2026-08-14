package sub

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Mihomo treats a proxy as UDP-incapable unless it says `udp: true`, and it then
// SKIPS the rule that selected it — the packet keeps matching and leaves DIRECT.
// That took Telegram calls out of the tunnel on mihomo clients on every lane except
// Hysteria2 (whose UDP support mihomo hardcodes), so every stream-based proxy we
// emit must carry the flag.
func TestClashStreamLanesRelayUDP(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	set := testSet("panel.example.com")
	ws := model.Inbound{
		ID: 7, Enabled: true, Name: "WS", Protocol: model.InbTrojan, Port: 8080,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/w"},
	}
	yaml := ClashYAMLMulti(u, []Server{{Set: set, Custom: []model.Inbound{ws}, Access: model.UnrestrictedAccess()}})

	block := yaml[strings.Index(yaml, "proxies:\n"):strings.Index(yaml, "proxy-groups:")]
	proxies := 0
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "  - {name: ") || strings.Contains(line, "type: hysteria2") {
			continue // hysteria2 is UDP by construction and has no such option
		}
		proxies++
		if !strings.Contains(line, "udp: true") {
			t.Errorf("proxy relays no UDP in mihomo:\n%s", line)
		}
	}
	if proxies != 3 { // built-in VLESS + built-in REALITY + the custom Trojan-WS
		t.Fatalf("expected 3 stream-based proxies, got %d:\n%s", proxies, yaml)
	}
}

// The MATCH rule must name the select group EXACTLY as the group defines it. It used
// to be written as `MATCH,%q`, which left the quotes inside the target — mihomo splits
// a rule on commas itself and never unquotes — and every client rejected the whole
// profile with `rules[N] [MATCH,"..."] error: proxy ["..."] not found`.
func TestClashMatchRuleNamesTheGroup(t *testing.T) {
	set := testSet("panel.example.com")
	set.SubTitle = "🐯 Freedom VPN, быстрый"
	set.SubNameInTitle = true
	u := model.User{ID: 1, Name: "Bellioz", UUID: "uuid", Password: "pw"}

	yaml := ClashYAMLMulti(u, One(set))
	group := clashGroupName(u, set)

	if strings.Contains(group, ",") {
		t.Fatalf("a comma in the group name would split the rule: %q", group)
	}
	if !strings.Contains(yaml, fmt.Sprintf("proxy-groups:\n  - {name: %q,", group)) {
		t.Fatalf("group %q missing from proxy-groups:\n%s", group, yaml)
	}
	if !strings.Contains(yaml, fmt.Sprintf("\n  - %q\n", "MATCH,"+group)) {
		t.Fatalf("MATCH rule does not target group %q:\n%s", group, yaml)
	}
	if strings.Contains(yaml, `  - MATCH,"`) {
		t.Fatalf("quotes leaked into the MATCH target:\n%s", yaml)
	}
}

// An emoji name has to survive every format the panel emits, not just the save.
//
// The name is embedded three different ways — a Go-quoted Clash scalar, a JSON
// string in sing-box, and a URL fragment in a share link — and each has its own way
// of mangling a four-byte rune. Allowing flags in the editor while one of these
// dropped them would be the worse bug, because it only shows up in the client.
func TestEmojiNamesSurviveEveryFormat(t *testing.T) {
	const name = "🇳🇱 Амстердам"
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	set := testSet("panel.example.com")
	in := model.Inbound{
		ID: 7, Enabled: true, Name: name, Protocol: model.InbTrojan, Port: 8080,
		Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/w"},
	}
	servers := []Server{{Set: set, Custom: []model.Inbound{in}, Access: model.UnrestrictedAccess()}}

	// Clash: %q keeps a printable rune as itself inside a quoted scalar.
	if yaml := ClashYAMLMulti(u, servers); !strings.Contains(yaml, name) {
		t.Errorf("clash dropped the emoji name:\n%s", yaml)
	}
	// sing-box: encoding/json, so the tag must round-trip through a decode.
	js := SingBoxJSONMulti(u, servers)
	var doc struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("sing-box output is not valid JSON: %v", err)
	}
	found := false
	for _, o := range doc.Outbounds {
		if o.Tag == name {
			found = true
		}
	}
	if !found {
		t.Errorf("sing-box has no outbound tagged %q:\n%s", name, js)
	}
	// Share link: the fragment is percent-escaped, and must decode back.
	links := ShareLinksAll(u, servers)
	labelled := false
	for _, l := range links {
		_, frag, ok := strings.Cut(l, "#")
		if !ok {
			t.Errorf("link carries no label: %s", l)
			continue
		}
		got, err := url.PathUnescape(frag)
		if err != nil {
			t.Errorf("fragment %q does not decode: %v", frag, err)
			continue
		}
		if got == name {
			labelled = true
		}
	}
	if !labelled {
		t.Errorf("no share link is labelled %q:\n%s", name, strings.Join(links, "\n"))
	}
}
