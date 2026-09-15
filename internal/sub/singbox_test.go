package sub

import (
	"encoding/json"
	"errors"
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
		out, err := SingBoxWithTemplate(tplUser(), tplServers(), c.tpl)
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
	if out, err := SingBoxWithTemplate(tplUser(), tplServers(), modern); err != nil || !strings.Contains(out, `"tag": "r"`) {
		t.Errorf("a current template did not render (err %v)", err)
	}
}
