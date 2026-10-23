package extsub

import (
	"strings"
	"testing"
)

const (
	wgPriv = "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
	wgPub  = "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg="
)

const wgConfText = `[Interface]
# Name = Germany
PrivateKey = ` + wgPriv + `
Address = 10.8.0.2/32, fd00::2/128
DNS = 1.1.1.1
MTU = 1380

[Peer]
PublicKey = ` + wgPub + `
PresharedKey = ` + wgPub + `
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = de.partner.example:51820

[Interface]
PrivateKey = ` + wgPriv + `
Address = 10.9.0.2/32
Jc = 4
S1 = 15

[Peer]
PublicKey = ` + wgPub + `
Endpoint = awg.partner.example:51820
`

// Pasted .conf files become wireguard:// links — the AmneziaWG one is left out — and
// each link reads back as a WireGuard endpoint whose Xray outbound carries the config.
func TestWireGuardConf(t *testing.T) {
	links := Decode([]byte(wgConfText))
	if len(links) != 1 {
		t.Fatalf("links %v, want the plain WireGuard config alone", links)
	}
	ep, ok := Parse(links[0])
	if !ok || ep.Protocol != "wireguard" || ep.Host != "de.partner.example" || ep.Port != 51820 || ep.Name != "Germany" {
		t.Fatalf("endpoint %+v %v", ep, ok)
	}
	o, ok := XrayOutbound(links[0], "ext-1")
	if !ok || o["protocol"] != "wireguard" {
		t.Fatalf("outbound %+v %v", o, ok)
	}
	s := o["settings"].(map[string]any)
	peer := s["peers"].([]map[string]any)[0]
	if s["secretKey"] != wgPriv || peer["publicKey"] != wgPub || peer["preSharedKey"] != wgPub ||
		peer["endpoint"] != "de.partner.example:51820" || s["mtu"] != 1380 || s["noKernelTun"] != true {
		t.Fatalf("settings %+v", s)
	}
	if addrs := s["address"].([]string); len(addrs) != 2 || addrs[0] != "10.8.0.2/32" {
		t.Fatalf("addresses %v", addrs)
	}
}

// A link is taken as the apps write it; one carrying AmneziaWG parameters, or a key
// that is not one, is not.
func TestWireGuardLink(t *testing.T) {
	good := "wireguard://" + strings.ReplaceAll(wgPriv, "+", "%2B") + "@1.2.3.4:51820?publickey=" +
		strings.ReplaceAll(wgPub, "+", "%2B") + "&address=10.0.0.2%2F32&reserved=1,2,3#NL"
	if ep, ok := Parse(good); !ok || ep.Name != "NL" {
		t.Fatalf("good link: %+v %v", ep, ok)
	}
	o, ok := XrayOutbound(good, "x")
	if !ok {
		t.Fatal("no outbound for a good link")
	}
	if r := o["settings"].(map[string]any)["reserved"].([]int); len(r) != 3 || r[2] != 3 {
		t.Fatalf("reserved %v", r)
	}
	for _, bad := range []string{
		strings.Replace(good, "&reserved", "&jc=4&reserved", 1),
		"wg://" + strings.Replace(strings.TrimPrefix(good, "wireguard://"), "#NL", "&enable_amnezia=true#NL", 1),
		strings.Replace(good, "publickey=", "publickey=short", 1),
	} {
		if _, ok := Parse(bad); ok {
			t.Errorf("taken: %s", bad)
		}
	}
}

// A renamed server keeps its link but for the label.
func TestWithName(t *testing.T) {
	if got := WithName("vless://id@1.2.3.4:443?security=tls#Old", "Германия"); got != "vless://id@1.2.3.4:443?security=tls#%D0%93%D0%B5%D1%80%D0%BC%D0%B0%D0%BD%D0%B8%D1%8F" {
		t.Fatalf("vless: %s", got)
	}
	vm := WithName("vmess://eyJhZGQiOiIxLjIuMy40IiwicG9ydCI6NDQzLCJpZCI6InUiLCJwcyI6Im9sZCJ9", "New")
	if ep, ok := Parse(vm); !ok || ep.Name != "New" || ep.Host != "1.2.3.4" {
		t.Fatalf("vmess: %+v %v", ep, ok)
	}
}

// A name written above its [Interface] labels that config, not the one before it; a
// key with a literal "+" in the link's query still reads.
func TestWireGuardConfNamesAndPlus(t *testing.T) {
	text := "# Name = First\n[Interface]\nPrivateKey = " + wgPriv + "\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = " + wgPub +
		"\nEndpoint = a.example:51820\n\n# Name = Second\n[Interface]\nPrivateKey = " + wgPriv + "\nAddress = 10.0.0.3/32\n[Peer]\nPublicKey = " + wgPub +
		"\nEndpoint = b.example:51820\n"
	var names []string
	for _, l := range WireGuardConfLinks(text) {
		ep, _ := Parse(l)
		names = append(names, ep.Name)
	}
	if len(names) != 2 || names[0] != "First" || names[1] != "Second" {
		t.Fatalf("names %q", names)
	}
	// The private key stands in as the peer's: any 32-byte key, and this one has a "+".
	raw := "wireguard://" + strings.ReplaceAll(wgPriv, "+", "%2B") + "@1.2.3.4:51820?publickey=" + wgPriv + "&address=10.0.0.2/32#x"
	if _, ok := Parse(raw); !ok {
		t.Fatal("a literal + in the public key broke the link")
	}
}
