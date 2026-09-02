package awg

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestKeysRoundTrip(t *testing.T) {
	priv, pub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if derived, err := PublicKey(priv); err != nil || derived != pub {
		t.Errorf("public key: %q (err %v), want %q", derived, err, pub)
	}
	h, err := keyHex(priv)
	if err != nil || len(h) != 64 {
		t.Errorf("hex: %q %v", h, err)
	}
	if _, err := PublicKey("not a key"); err == nil {
		t.Error("garbage accepted as a key")
	}
	if _, err := PublicKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("a short key was accepted")
	}
}

func TestRandomParamsAreValidAndDistinct(t *testing.T) {
	for range 200 {
		p := RandomParams()
		if err := p.Validate(); err != nil {
			t.Fatalf("random params invalid: %v (%+v)", err, p)
		}
	}
	a, b := RandomParams(), RandomParams()
	if a == b {
		t.Error("two random parameter sets came out identical")
	}
	bad := []Params{
		{Jc: 129, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, H1: 5, H2: 6, H3: 7, H4: 8},
		{Jc: 3, Jmin: 1001, Jmax: 1000, S1: 10, S2: 20, H1: 5, H2: 6, H3: 7, H4: 8},
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 66, H1: 5, H2: 6, H3: 7, H4: 8}, // s1+56 == s2
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, H1: 5, H2: 5, H3: 7, H4: 8},
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, H1: 1, H2: 6, H3: 7, H4: 8},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("bad params %d accepted: %+v", i, p)
		}
	}
}

func TestClientAddr(t *testing.T) {
	if a, ok := ClientAddr(1); !ok || a.String() != "10.66.0.2" {
		t.Errorf("user 1: %v %v", a, ok)
	}
	if a, ok := ClientAddr(254); !ok || a.String() != "10.66.0.255" {
		t.Errorf("user 254: %v %v", a, ok)
	}
	if a, ok := ClientAddr(255); !ok || a.String() != "10.66.1.0" {
		t.Errorf("user 255: %v %v", a, ok)
	}
	if _, ok := ClientAddr(0); ok {
		t.Error("user 0 got an address")
	}
	if _, ok := ClientAddr(65534); ok {
		t.Error("an id past the subnet got an address")
	}
	// Every address is inside the subnet and distinct.
	seen := map[string]bool{}
	for id := int64(1); id < 3000; id++ {
		a, ok := ClientAddr(id)
		if !ok || !Subnet.Contains(a) || a == ServerAddr || seen[a.String()] {
			t.Fatalf("user %d: %v ok=%v", id, a, ok)
		}
		seen[a.String()] = true
	}
}

func TestUAPIAndClientConfig(t *testing.T) {
	sPriv, sPub, _ := GenerateKey()
	cPriv, cPub, _ := GenerateKey()
	params := Params{Jc: 4, Jmin: 50, Jmax: 1000, S1: 30, S2: 40, H1: 11, H2: 12, H3: 13, H4: 14}
	addr, _ := ClientAddr(7)
	cfg := Config{PrivateKey: sPriv, ListenPort: 51820, Params: params,
		Peers: []Peer{{PublicKey: cPub, Addr: addr, Email: "u7"}}}
	uapi, err := cfg.UAPI()
	if err != nil {
		t.Fatal(err)
	}
	sHex, _ := keyHex(sPriv)
	cHex, _ := keyHex(cPub)
	for _, want := range []string{
		"private_key=" + sHex, "listen_port=51820", "jc=4\njmin=50\njmax=1000\ns1=30\ns2=40\nh1=11\nh2=12\nh3=13\nh4=14",
		"replace_peers=true", "public_key=" + cHex, "allowed_ip=10.66.0.8/32",
	} {
		if !strings.Contains(uapi, want) {
			t.Errorf("uapi lacks %q:\n%s", want, uapi)
		}
	}
	// Peers are emitted in a stable order regardless of input order.
	p2Priv, p2Pub, _ := GenerateKey()
	_ = p2Priv
	a := Config{PrivateKey: sPriv, ListenPort: 1, Params: params, Peers: []Peer{{cPub, addr, "u7"}, {p2Pub, addr, "u8"}}}
	b := Config{PrivateKey: sPriv, ListenPort: 1, Params: params, Peers: []Peer{{p2Pub, addr, "u8"}, {cPub, addr, "u7"}}}
	ua, _ := a.UAPI()
	ub, _ := b.UAPI()
	if ua != ub {
		t.Error("peer order leaked into the UAPI text")
	}
	if _, err := (Config{PrivateKey: sPriv, ListenPort: 0, Params: params}).UAPI(); err == nil {
		t.Error("port 0 accepted")
	}
	if _, err := (Config{PrivateKey: sPriv, ListenPort: 1, Params: params, Peers: []Peer{{PublicKey: "bad", Addr: addr}}}).UAPI(); err == nil {
		t.Error("a bad peer key was accepted")
	}

	conf := ClientConfig{PrivateKey: cPriv, Address: addr, Params: params, ServerPublicKey: sPub, Endpoint: "vpn.example.com:51820"}.Render()
	for _, want := range []string{
		"[Interface]", "PrivateKey = " + cPriv, "Address = 10.66.0.8/32", "DNS = " + DefaultDNS, "MTU = 1420",
		"Jc = 4", "H4 = 14", "[Peer]", "PublicKey = " + sPub, "AllowedIPs = 0.0.0.0/0, ::/0",
		"Endpoint = vpn.example.com:51820", "PersistentKeepalive = 25",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("client config lacks %q:\n%s", want, conf)
		}
	}
}

func TestParseStats(t *testing.T) {
	_, pub, _ := GenerateKey()
	raw, _ := base64.StdEncoding.DecodeString(pub)
	dump := "private_key=00\nlisten_port=51820\n" +
		"public_key=" + hex.EncodeToString(raw) + "\nendpoint=203.0.113.9:40001\nlast_handshake_time_sec=1700000000\n" +
		"last_handshake_time_nsec=5\ntx_bytes=1234\nrx_bytes=99\npersistent_keepalive_interval=0\nallowed_ip=10.66.0.2/32\n" +
		"public_key=zz\nrx_bytes=1\nerrno=0\n"
	st := ParseStats(dump)
	if len(st) != 1 {
		t.Fatalf("want one parsable peer, got %v", st)
	}
	p := st[pub]
	if p.RxBytes != 99 || p.TxBytes != 1234 || p.LastHandshake != 1700000000 || p.Endpoint != "203.0.113.9:40001" {
		t.Errorf("stats: %+v", p)
	}
	if EndpointIP(p.Endpoint) != "203.0.113.9" || EndpointIP("[2001:db8::1]:5") != "2001:db8::1" || EndpointIP("") != "" {
		t.Error("endpoint ip")
	}
}
