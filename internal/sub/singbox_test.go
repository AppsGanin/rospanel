package sub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

type singboxDoc struct {
	DNS struct {
		Servers []map[string]any `json:"servers"`
		Rules   []any            `json:"rules"`
	} `json:"dns"`
	Route map[string]any `json:"route"`
}

func decodeSingBox(t *testing.T, js string) singboxDoc {
	t.Helper()
	var doc singboxDoc
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("profile is not JSON: %v\n%s", err, js)
	}
	return doc
}

// sing-box 1.14 removed the one-string DNS server form ("address": "https://…") and
// refuses the whole profile over it — 1.13 already did unless an env var says
// otherwise. Issue #89: every sing-box user lost every server at once. Each server
// must be in the typed form, and a profile dialing a domain must name the resolver
// for it, which 1.14 also refuses to go without.
func TestSingBoxDNSUsesTypedServers(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	domain := testSet("panel.example.com")
	node := testSet("146.103.108.248")

	doc := decodeSingBox(t, SingBoxJSONMulti(u, []Server{
		{Set: domain, Access: model.UnrestrictedAccess()},
		{Set: node, Access: model.UnrestrictedAccess()},
	}))
	tags := map[string]map[string]any{}
	for _, s := range doc.DNS.Servers {
		if _, legacy := s["address"]; legacy {
			t.Errorf("DNS server in the removed address form: %v", s)
		}
		if s["type"] == nil || s["server"] == nil {
			t.Errorf("DNS server without type/server: %v", s)
		}
		tags[s["tag"].(string)] = s
	}
	resolver, _ := doc.Route["default_domain_resolver"].(string)
	boot, ok := tags[resolver]
	if !ok {
		t.Fatalf("route.default_domain_resolver = %q names no DNS server (have %v)", resolver, doc.Route)
	}
	// The server's own name has to resolve before the tunnel exists: a resolver that
	// detours through the proxy group deadlocks the first connect.
	if boot["detour"] != nil {
		t.Errorf("domain resolver %q detours through %v — it cannot, the tunnel is not up yet", resolver, boot["detour"])
	}
	// 223.5.5.5 hangs after the ClientHello from Russian networks; a profile using it
	// never resolved its server and never connected.
	if boot["server"] == "223.5.5.5" {
		t.Error("bootstrap is back on 223.5.5.5, which does not answer from Russia")
	}
	if len(doc.DNS.Rules) != 0 {
		t.Errorf("host-matching DNS rules are the pre-1.12 resolver mechanism: %v", doc.DNS.Rules)
	}
}

// An all-IP profile resolves no server name, so it carries neither the bootstrap
// server nor a resolver pointing at one.
func TestSingBoxAllIPHasNoBootstrap(t *testing.T) {
	u := model.User{ID: 1, Name: "u", UUID: "uuid", Password: "pw"}
	doc := decodeSingBox(t, SingBoxJSONMulti(u, One(testSet("144.31.159.81"))))
	if len(doc.DNS.Servers) != 1 || doc.DNS.Servers[0]["tag"] != "remote" {
		t.Errorf("all-IP profile DNS servers = %v, want only remote", doc.DNS.Servers)
	}
	if r, ok := doc.Route["default_domain_resolver"]; ok {
		t.Errorf("all-IP profile names a domain resolver %v with nothing to resolve", r)
	}
}

// Every case here is a field sing-box 1.13 or 1.14 refuses to load (checked against
// the binaries), in a template an operator wrote for an older client. Saving it must
// be refused with the field named; serving one saved earlier must fall back to the
// generated profile, which loads.
func TestSingBoxTemplateRemovedFields(t *testing.T) {
	cases := map[string]struct{ tpl, path string }{
		"legacy dns server": {`{"dns":{"servers":[{"tag":"a","type":"udp","server":"1.1.1.1"},{"tag":"b","address":"https://8.8.8.8/dns-query"}]},"outbounds":["{{proxies}}"]}`, "dns.servers[1].address"},
		"dns rule outbound": {`{"dns":{"rules":[{"outbound":"any","server":"a"}]},"outbounds":["{{proxies}}"]}`, "dns.rules[0].outbound"},
		"dns outbound":      {`{"outbounds":["{{proxies}}",{"type":"dns","tag":"dns-out"}]}`, "outbounds[1].type=dns"},
		"wireguard":         {`{"outbounds":[{"type":"wireguard","tag":"wg"},"{{proxies}}"]}`, "outbounds[0].type=wireguard"},
		"inbound sniff":     {`{"inbounds":[{"type":"tun","address":["172.19.0.1/30"],"sniff":true}],"outbounds":["{{proxies}}"]}`, "inbounds[0].sniff"},
		"tun inet4":         {`{"inbounds":[{"type":"tun","inet4_address":"172.19.0.1/30"}],"outbounds":["{{proxies}}"]}`, "inbounds[0].inet4_address"},
		"nested geosite":    {`{"outbounds":["{{proxies}}"],"route":{"rules":[{"type":"logical","mode":"or","rules":[{"domain":["a"]},{"geosite":["ru"]}],"outbound":"direct"}]}}`, "route.rules[0].rules[1].geosite"},
	}
	for name, c := range cases {
		err := ValidateSingBoxTemplate(c.tpl)
		var legacy *SingBoxLegacyError
		if !errors.As(err, &legacy) || legacy.Path != c.path {
			t.Errorf("%s: validate = %v, want a removed-field error at %s", name, err, c.path)
		}
		out, err := SingBoxWithTemplate(tplUser(), tplServers(), c.tpl, false)
		if !errors.Is(err, ErrSingBoxLegacy) || out != SingBoxJSONMulti(tplUser(), tplServers()) {
			t.Errorf("%s: a stored template with a removed field must serve the generated profile (err %v)", name, err)
		}
	}

	// The current shapes of the same things pass: a route rule's outbound is an action,
	// the block outbound still loads on 1.14, and the typed DNS form is the new one.
	modern := `{"dns":{"servers":[{"type":"https","tag":"r","server":"1.1.1.1"}]},
		"inbounds":[{"type":"tun","address":["172.19.0.1/30"]}],
		"outbounds":["{{proxies}}",{"type":"block","tag":"block"}],
		"route":{"rules":[{"action":"sniff"},{"ip_is_private":true,"outbound":"direct"}]}}`
	if err := ValidateSingBoxTemplate(modern); err != nil {
		t.Errorf("a current template was refused: %v", err)
	}
	if out, err := SingBoxWithTemplate(tplUser(), tplServers(), modern, false); err != nil || !strings.Contains(out, `"tag": "r"`) {
		t.Errorf("a current template did not render (err %v)", err)
	}
}

func TestSingBoxNaiveClients(t *testing.T) {
	for ua, want := range map[string]bool{
		"SFA (sing-box 1.13.21; language en_US)":                    true,
		"SFI (sing-box 1.14.3; language ru_RU)":                     true,
		"SFM (sing-box 2.0.0; language en)":                         true,
		"SFT (sing-box 1.13.0; language en)":                        true,
		"SFA (sing-box 1.12.9; language en_US)":                     false,
		"sing-box 1.14.3":                                           false, // the CLI: cronet is not in every build
		"HiddifyNext/2.5.7 (android) like ClashMeta v2ray sing-box": false,
		"husi/0.15.5 (sing-box 1.13.4)":                             true,
		"husi/0.15.2 (sing-box 1.13.0)":                             false, // naive, but no udp_over_tcp yet
		"":                                                          false,
	} {
		if got := SingBoxNaive(ua); got != want {
			t.Errorf("%q: %v, want %v", ua, got, want)
		}
	}
}

// NaiveProxy is an outbound like the others — in the selector and in urltest — and carries
// UDP over TCP.
func TestSingBoxNaiveOutbound(t *testing.T) {
	set := &model.Settings{Host: "203.0.113.5", SNI: "vpn.example.com", VLESSPort: 443,
		VLESSEnabled: true, NaiveEnabled: true}
	u := model.User{ID: 3, UUID: "11111111-2222-3333-4444-555555555555"}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}

	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(singboxJSON(u, servers, true)), &doc); err != nil {
		t.Fatal(err)
	}
	var naive, selector, auto map[string]any
	for _, o := range doc.Outbounds {
		switch o["type"] {
		case "naive":
			naive = o
		case "selector":
			selector = o
		case "urltest":
			auto = o
		}
	}
	if naive == nil {
		t.Fatal("no naive outbound")
	}
	tls := naive["tls"].(map[string]any)
	if naive["server"] != "203.0.113.5" || naive["server_port"] != float64(443) || naive["username"] != "u3" ||
		naive["password"] != u.UUID || tls["server_name"] != "vpn.example.com" {
		t.Fatalf("naive outbound %v", naive)
	}
	if !strings.Contains(fmt.Sprint(selector["outbounds"]), fmt.Sprint(naive["tag"])) {
		t.Fatalf("selector %v lacks %v", selector["outbounds"], naive["tag"])
	}
	if !strings.Contains(fmt.Sprint(auto["outbounds"]), fmt.Sprint(naive["tag"])) {
		t.Fatalf("urltest %v lacks %v", auto["outbounds"], naive["tag"])
	}
	if naive["udp_over_tcp"] != true {
		t.Fatalf("naive outbound without UDP over TCP: %v", naive)
	}

	// Not for a client that cannot load it, nor on a self-signed certificate.
	if strings.Contains(singboxJSON(u, servers, false), `"naive"`) {
		t.Fatal("naive outbound without client support")
	}
	set.TLSInsecure = true
	if strings.Contains(singboxJSON(u, servers, true), `"naive"`) {
		t.Fatal("naive outbound on a self-signed certificate")
	}
}

// With VLESS-TCP-TLS off and NaiveProxy on, the user gets NaiveProxy alone.
func TestNaiveWithoutVLESS(t *testing.T) {
	set := &model.Settings{Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443, NaiveEnabled: true}
	u := model.User{ID: 3, UUID: "11111111-2222-3333-4444-555555555555"}
	srv := Server{Set: set, Access: model.UnrestrictedAccess()}
	links := ShareLinks(u, srv)
	if len(links) != 1 || !strings.HasPrefix(links[0], "naive+https://u3:11111111-2222-3333-4444-555555555555@vpn.example.com:443#") {
		t.Fatalf("links %v", links)
	}
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(singboxJSON(u, []Server{srv}, true)), &doc); err != nil {
		t.Fatal(err)
	}
	var types []any
	for _, o := range doc.Outbounds {
		types = append(types, o["type"])
	}
	if fmt.Sprint(types) != "[selector urltest naive direct]" {
		t.Fatalf("outbounds %v", types)
	}
}

// A template whose DNS would leave the naive outbound's server unresolvable — two
// servers, no default resolver — gets no naive outbound: sing-box would refuse the
// whole profile over it.
func TestSingBoxTemplateNaiveNeedsAResolver(t *testing.T) {
	set := &model.Settings{Host: "vpn.example.com", SNI: "vpn.example.com", VLESSPort: 443,
		VLESSEnabled: true, NaiveEnabled: true}
	u := model.User{ID: 3, UUID: "11111111-2222-3333-4444-555555555555"}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}
	const body = `"outbounds": [{"type": "selector", "tag": "x", "outbounds": ["{{tags}}"]}, "{{proxies}}"]`
	for name, c := range map[string]struct {
		tpl   string
		naive bool
	}{
		"two servers, no resolver": {`{"dns": {"servers": [{"type": "https", "tag": "a", "server": "1.1.1.1"}, {"type": "local", "tag": "b"}]}, ` + body + `}`, false},
		"two servers, a resolver":  {`{"dns": {"servers": [{"type": "https", "tag": "a", "server": "1.1.1.1"}, {"type": "local", "tag": "b"}]}, "route": {"default_domain_resolver": "b"}, ` + body + `}`, true},
		"one server":               {`{"dns": {"servers": [{"type": "local", "tag": "b"}]}, ` + body + `}`, true},
		"no dns":                   {`{` + body + `}`, true},
	} {
		out, err := SingBoxWithTemplate(u, servers, c.tpl, true)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := strings.Contains(out, `"naive"`); got != c.naive {
			t.Errorf("%s: naive outbound %v, want %v", name, got, c.naive)
		}
	}
}

// Shadowrocket gets its NaiveProxy server as http2://base64(user:pass@host:port), the
// form its HTTP2 type imports; every other client naive+https://.
func TestNaiveLinkForShadowrocket(t *testing.T) {
	set := &model.Settings{Host: "203.0.113.5", SNI: "vpn.example.com", VLESSPort: 443, NaiveEnabled: true}
	u := model.User{ID: 3, UUID: "11111111-2222-3333-4444-555555555555"}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}

	links := ShareLinksFor(u, servers, "Shadowrocket/2070 CFNetwork/1568.200.51 Darwin/24.1.0")
	if len(links) != 1 || !strings.HasPrefix(links[0], "http2://") {
		t.Fatalf("Shadowrocket links %v", links)
	}
	pu, err := url.Parse(links[0])
	if err != nil {
		t.Fatal(err)
	}
	auth, err := base64.StdEncoding.DecodeString(pu.Host)
	if err != nil {
		t.Fatalf("payload %q: %v", pu.Host, err)
	}
	if string(auth) != "u3:11111111-2222-3333-4444-555555555555@203.0.113.5:443" {
		t.Fatalf("payload %q", auth)
	}
	if q := pu.Query(); q.Get("padding") != "1" || q.Get("peer") != "vpn.example.com" {
		t.Fatalf("query %v", q)
	}
	if pu.Fragment != "NAIVEPROXY" {
		t.Fatalf("label %q", pu.Fragment)
	}

	// The clients whose parsers read naive+https:// get it; an empty UA is a hand-made
	// request and gets it too.
	for _, ua := range []string{"v2rayN/7.25.6", "Karing/1.2.25.2802 platform/android",
		"NekoBox/Android/1.4.2 (Prefer ClashMeta Format)", ""} {
		if l := ShareLinksFor(u, servers, ua); len(l) != 1 || !strings.HasPrefix(l[0], "naive+https://") {
			t.Fatalf("%q: links %v", ua, l)
		}
	}
	// Clients without NaiveProxy get no such line at all.
	for _, ua := range []string{"Happ/3.5.1/ios/18.2", "v2rayNG/2.3.11", "INCY/1.4.0/android",
		"v2raytun/5.25.81 (android)", "V2Box/10.1.9", "Streisand/1.6.76"} {
		if l := ShareLinksFor(u, servers, ua); len(l) != 0 {
			t.Fatalf("%q got %v", ua, l)
		}
	}
}
