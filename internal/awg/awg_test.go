package awg

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
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
	// One header per message type, well apart, as every case below starts from.
	h := func() (Range, Range, Range, Range) {
		return Range{5, 5}, Range{6, 6}, Range{7, 7}, Range{8, 8}
	}
	h1, h2, h3, h4 := h()
	bad := []Params{
		{Jc: 129, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, H1: h1, H2: h2, H3: h3, H4: h4},
		{Jc: 3, Jmin: 1001, Jmax: 1000, S1: 10, S2: 20, H1: h1, H2: h2, H3: h3, H4: h4},
		// A padded init and a padded response of the same length: s1+56 == s2.
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 66, H1: h1, H2: h2, H3: h3, H4: h4},
		// A padded init and a padded cookie of the same length: s3 == s1+84.
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, S3: 94, H1: h1, H2: h2, H3: h3, H4: h4},
		// A padded response and a padded cookie of the same length: s3 == s2+28.
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, S3: 48, H1: h1, H2: h2, H3: h3, H4: h4},
		// Two message types that could be read as one another.
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, H1: h1, H2: h1, H3: h3, H4: h4},
		// Header bands that overlap, which is the same fault written as a range.
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, H1: Range{100, 200}, H2: Range{150, 250}, H3: h3, H4: h4},
		// Inside WireGuard's own message types 1–4.
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, H1: Range{1, 1}, H2: h2, H3: h3, H4: h4},
		// A header key that is not 32 bytes of base64.
		{Jc: 3, Jmin: 50, Jmax: 1000, S1: 10, S2: 20, H1: h1, H2: h2, H3: h3, H4: h4, HeaderKey: "not-a-key"},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("bad params %d accepted: %+v", i, p)
		}
	}
}

// A generated block must actually be 3.1: the header protection is the whole
// reason for the move, and a set that quietly came out without it would look
// fine, validate fine and obfuscate less than the release it claims to be.
func TestRandomParamsAre31(t *testing.T) {
	p := RandomParams()
	switch {
	case p.HeaderKey == "":
		t.Error("no header protection key")
	case p.S3 == 0 || p.S4 == 0:
		t.Error("cookie and transport messages left unpadded")
	case p.H1.Min == p.H1.Max:
		t.Error("headers are single values, not bands")
	case p.Padding.IsZero() || !p.Trailers:
		t.Error("transport padding or trailers missing")
	case p.RekeyAfter.IsZero() || p.RejectAfter.IsZero() || p.Keepalive.IsZero():
		t.Error("timers left at their fixed defaults")
	}
	if _, err := keyBytes(p.HeaderKey); err != nil {
		t.Errorf("header key is not a 32-byte base64 key: %v", err)
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
	// A parameter block in the shape every row written before 3.1 holds: single
	// header values and none of the 3.1 fields.
	params := Params{Jc: 4, Jmin: 50, Jmax: 1000, S1: 30, S2: 40,
		H1: Range{11, 11}, H2: Range{12, 12}, H3: Range{13, 13}, H4: Range{14, 14}}
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
	// A 1.5 block must render as it always did. Emitting a 3.1 key with the
	// engine's default behind it would change how an existing tunnel behaves —
	// and the client, whose config would not have grown to match, would drop off.
	for _, unwanted := range []string{"s3=", "s4=", "header_protection_key=", "content_padding_addition=",
		"random_trailers=", "rekey_after_time=", "reject_after_time=", "keepalive_timeout=", "max_handshake_attempts="} {
		if strings.Contains(uapi, unwanted) {
			t.Errorf("a pre-3.1 block emitted %q:\n%s", unwanted, uapi)
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
	for _, unwanted := range []string{"S3 =", "S4 =", "HeaderProtectionKey", "ContentPaddingAddition",
		"RandomTrailers", "RekeyAfterTime", "RejectAfterTime", "KeepaliveTimeout", "MaxHandshakeAttempts"} {
		if strings.Contains(conf, unwanted) {
			t.Errorf("a pre-3.1 block emitted %q:\n%s", unwanted, conf)
		}
	}
}

// The 3.1 half of both emitters, key by key. The spellings are not ours to
// choose — the left column is what amneziawg-go's IPC reads and the right is what
// amneziawg-tools parses out of the file — so a typo here is a tunnel that comes
// up refusing every client, and only a test that names them catches it.
func TestUAPIAndClientConfigCarryThe31Parameters(t *testing.T) {
	sPriv, sPub, _ := GenerateKey()
	cPriv, cPub, _ := GenerateKey()
	p := RandomParams()
	if err := p.Validate(); err != nil {
		t.Fatalf("generated params invalid: %v", err)
	}
	addr, _ := ClientAddr(7)
	uapi, err := Config{PrivateKey: sPriv, ListenPort: 51820, Params: p,
		Peers: []Peer{{PublicKey: cPub, Addr: addr, Email: "u7"}}}.UAPI()
	if err != nil {
		t.Fatal(err)
	}
	hkHex, _ := keyHex(p.HeaderKey)
	for _, want := range []string{
		fmt.Sprintf("s3=%d", p.S3),
		fmt.Sprintf("s4=%d", p.S4),
		"h1=" + p.H1.String(),
		"header_protection_key=" + hkHex,
		"content_padding_addition=" + p.Padding.String(),
		"random_trailers=true",
		"rekey_after_time=" + p.RekeyAfter.String(),
		"rekey_timeout=" + p.RekeyTimeout.String(),
		"reject_after_time=" + p.RejectAfter.String(),
		"keepalive_timeout=" + p.Keepalive.String(),
		"max_handshake_attempts=" + p.Handshakes.String(),
	} {
		if !strings.Contains(uapi, want) {
			t.Errorf("uapi lacks %q:\n%s", want, uapi)
		}
	}

	conf := ClientConfig{PrivateKey: cPriv, Address: addr, Params: p,
		ServerPublicKey: sPub, Endpoint: "vpn.example.com:51820"}.Render()
	for _, want := range []string{
		fmt.Sprintf("S3 = %d", p.S3),
		fmt.Sprintf("S4 = %d", p.S4),
		"H1 = " + p.H1.String(),
		// Base64 in the file, hex over the IPC — the same split as the private key.
		"HeaderProtectionKey = " + p.HeaderKey,
		"ContentPaddingAddition = " + p.Padding.String(),
		"RandomTrailers = on",
		"RekeyAfterTime = " + p.RekeyAfter.String(),
		"RekeyTimeout = " + p.RekeyTimeout.String(),
		"RejectAfterTime = " + p.RejectAfter.String(),
		"KeepaliveTimeout = " + p.Keepalive.String(),
		"MaxHandshakeAttempts = " + p.Handshakes.String(),
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
