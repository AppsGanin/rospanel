package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
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

// A Shadowsocks-2022 inbound has to come out consistent in all three client formats:
// the ss:// link, the Clash proxy and the sing-box outbound. The password everywhere
// is "serverKey:userKey", and the ss:// userinfo is that triple with the method,
// base64url. A mismatch between any two would hand the user a config that fails to
// authenticate on exactly one of their apps.
func TestShadowsocksEveryFormat(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid-ss", Password: "pw"}
	set := testSet("panel.example.com")
	in := model.Inbound{
		ID: 9, Enabled: true, Name: "SS", Protocol: model.InbShadowsocks, Port: 9500,
		Opts: model.InboundOpts{
			Method: model.SS2022AES128, ShadowKey: base64.StdEncoding.EncodeToString(make([]byte, 16)),
		},
	}
	in.Normalize()
	servers := []Server{{Set: set, Custom: []model.Inbound{in}, Access: model.UnrestrictedAccess()}}

	userKey := model.UserShadowKey(u.UUID, model.SS2022AES128)
	wantPw := in.Opts.ShadowKey + ":" + userKey

	// Clash: type ss, cipher = method, password = serverKey:userKey, udp on.
	yaml := ClashYAMLMulti(u, servers)
	if !strings.Contains(yaml, "type: ss") || !strings.Contains(yaml, "cipher: "+model.SS2022AES128) {
		t.Errorf("clash SS entry missing or wrong:\n%s", yaml)
	}
	if !strings.Contains(yaml, wantPw) {
		t.Errorf("clash password is not serverKey:userKey:\n%s", yaml)
	}

	// sing-box: type shadowsocks, method + same password.
	js := SingBoxJSONMulti(u, servers)
	var doc struct {
		Outbounds []struct {
			Type     string `json:"type"`
			Method   string `json:"method"`
			Password string `json:"password"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("sing-box output invalid: %v", err)
	}
	found := false
	for _, o := range doc.Outbounds {
		if o.Type == "shadowsocks" {
			found = true
			if o.Method != model.SS2022AES128 || o.Password != wantPw {
				t.Errorf("sing-box SS = method %q pw %q, want %q / %q",
					o.Method, o.Password, model.SS2022AES128, wantPw)
			}
		}
	}
	if !found {
		t.Errorf("sing-box has no shadowsocks outbound:\n%s", js)
	}

	// ss:// link: userinfo decodes to method:serverKey:userKey.
	var ssLink string
	for _, l := range ShareLinksAll(u, servers) {
		if strings.HasPrefix(l, "ss://") {
			ssLink = l
		}
	}
	if ssLink == "" {
		t.Fatal("no ss:// link produced")
	}
	rest := strings.TrimPrefix(ssLink, "ss://")
	userinfo, _, _ := strings.Cut(rest, "@")
	raw, err := base64.RawURLEncoding.DecodeString(userinfo)
	if err != nil {
		t.Fatalf("ss userinfo is not base64url: %v", err)
	}
	if want := model.SS2022AES128 + ":" + in.Opts.ShadowKey + ":" + userKey; string(raw) != want {
		t.Errorf("ss userinfo = %q, want %q", raw, want)
	}
	if !strings.Contains(ssLink, "@panel.example.com:9500#") {
		t.Errorf("ss link host/port/label wrong: %s", ssLink)
	}
}
