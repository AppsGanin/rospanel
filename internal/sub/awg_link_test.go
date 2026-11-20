package sub

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
)

func TestAWGLinkClients(t *testing.T) {
	for ua, want := range map[string]bool{
		"INCY/2.6.2/ios":      true,
		"INCY/2.7.0/iOS":      true,
		"INCY/2.5.9/ios":      false, // before the 3.1 engine was documented
		"INCY/3.7.0/android":  true,
		"INCY/3.6.4/android":  false,
		"INCY/3.8.8/windows":  true, // the desktop build ignores the line
		"Happ/3.5.1/ios/18.2": false,
		"v2rayNG/2.3.11":      false,
		"":                    false,
	} {
		if got := AWGLinkFor(ua); got != want {
			t.Errorf("%q: %v, want %v", ua, got, want)
		}
	}
}

// INCY gets the tunnel as an amneziawg:// line carrying exactly the .conf the page hands
// out; a client that cannot read 3.1 gets no such line.
func TestAWGIncyLink(t *testing.T) {
	_, pub, _ := awg.GenerateKey()
	priv, _, _ := awg.GenerateKey()
	set := &model.Settings{Host: "vpn.example.com", VLESSPort: 443, VLESSEnabled: true, AWGEnabled: true,
		AWGPort: 51999, AWGPublicKey: pub, AWGParams: awg.ToModel(awg.RandomParams())}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555", WGPrivateKey: priv, AWGSlot: 9}
	servers := []Server{{Set: set, Access: model.UnrestrictedAccess()}}

	links := ShareLinksFor(u, servers, "INCY/3.7.0/android")
	var line string
	for _, l := range links {
		if strings.HasPrefix(l, "amneziawg://") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no amneziawg:// line for INCY: %v", links)
	}
	pu, err := url.Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	conf, err := base64.RawURLEncoding.DecodeString(pu.Host)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if string(conf) != AWGClientConf(u, set) {
		t.Fatalf("the line carries a different config:\n%s", conf)
	}
	for _, want := range []string{"HeaderProtectionKey = ", "RandomTrailers = on", "Address = 10.66.0.9/32",
		"Endpoint = vpn.example.com:51999"} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("config lacks %q", want)
		}
	}
	if pu.Fragment != model.ProtoAWG {
		t.Errorf("label %q", pu.Fragment)
	}

	for _, ua := range []string{"Happ/3.5.1/ios/18.2", "v2rayNG/2.3.11", "INCY/3.6.4/android"} {
		for _, l := range ShareLinksFor(u, servers, ua) {
			if strings.HasPrefix(l, "amneziawg://") {
				t.Errorf("%q got %s", ua, l)
			}
		}
	}
	// Not to a user without a tunnel identity, nor against the user's groups.
	for _, l := range ShareLinksFor(model.User{ID: 8, UUID: u.UUID}, servers, "INCY/3.7.0/android") {
		if strings.HasPrefix(l, "amneziawg://") {
			t.Error("a user without a tunnel key got the line")
		}
	}
	limited := []Server{{Set: set, Access: model.Access{Tokens: map[string]bool{model.BuiltinToken(0, model.LaneVLESS): true}}}}
	for _, l := range ShareLinksFor(u, limited, "INCY/3.7.0/android") {
		if strings.HasPrefix(l, "amneziawg://") {
			t.Error("AmneziaWG handed out against the user's groups")
		}
	}
}

// A host with a line break never reaches a config: it would add lines of its own.
func TestAWGConfRefusesABrokenHost(t *testing.T) {
	_, pub, _ := awg.GenerateKey()
	priv, _, _ := awg.GenerateKey()
	set := &model.Settings{Host: "203.0.113.5\n[Interface]\nPreUp = id #", AWGEnabled: true, AWGPort: 51999,
		AWGPublicKey: pub, AWGParams: awg.ToModel(awg.RandomParams())}
	u := model.User{ID: 7, WGPrivateKey: priv, AWGSlot: 9}
	if conf := AWGClientConf(u, set); conf != "" {
		t.Fatalf("config built for a broken host:\n%s", conf)
	}
}

// Throne's wg:// carries the whole 3.1 set its parser reads, keys as they are, and is
// handed to Throne from 1.3.0 only.
func TestAWGThroneLink(t *testing.T) {
	_, pub, _ := awg.GenerateKey()
	priv, _, _ := awg.GenerateKey()
	s := &model.Settings{Host: "vpn.example.com", AWGEnabled: true, AWGPort: 51999, AWGPublicKey: pub,
		AWGParams: awg.ToModel(awg.RandomParams())}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555", WGPrivateKey: priv, AWGSlot: 9}
	l := AWGThroneLink(u, s)
	pu, err := url.Parse(l)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := awg.FromModel(s.AWGParams)
	q := pu.Query()
	if pu.Scheme != "wg" || pu.Port() != strconv.Itoa(s.AWGPort) || q.Get("enable_amnezia") != "true" ||
		q.Get("mtu") != "1280" ||
		q.Get("h1") != p.H1.String() || q.Get("s3") != strconv.Itoa(p.S3) ||
		q.Get("content_padding_addition") != p.Padding.String() || q.Get("i1") != "" {
		t.Fatalf("link %s", l)
	}
	if p.Trailers && q.Get("random_trailers") != "true" {
		t.Fatalf("no random_trailers: %s", l)
	}
	// Base64 as is: "+" would read as a space through url.Query, so compare the raw text.
	if !strings.Contains(l, "private_key="+u.WGPrivateKey+"&") || !strings.Contains(l, "public_key="+s.AWGPublicKey+"&") ||
		p.HeaderKey != "" && !strings.Contains(l, "header_protection_key="+p.HeaderKey+"&") {
		t.Fatalf("keys not as they are: %s", l)
	}
	for ua, want := range map[string]bool{"Throne/1.3.0": true, "Throne/1.2.2": false, "INCY/3.7.0/android": true, "v2rayNG/1.10.0": false} {
		if got := awgLinkFor(ua) != nil; got != want {
			t.Errorf("%q: %v, want %v", ua, got, want)
		}
	}
}

// Shadowrocket's wg:// carries the keys as named query values and the AmneziaWG set as
// one JSON object of strings, bands as "min-max"; every Shadowrocket gets it.
func TestAWGShadowrocketLink(t *testing.T) {
	_, pub, _ := awg.GenerateKey()
	priv, _, _ := awg.GenerateKey()
	s := &model.Settings{Host: "vpn.example.com", AWGEnabled: true, AWGPort: 51999, AWGPublicKey: pub,
		AWGParams: awg.ToModel(awg.RandomParams())}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555", WGPrivateKey: priv, AWGSlot: 9}
	l := AWGShadowrocketLink(u, s)
	pu, err := url.Parse(l)
	if err != nil {
		t.Fatal(err)
	}
	q := pu.Query()
	if pu.Scheme != "wg" || pu.Host != "vpn.example.com:51999" || q.Get("publicKey") != pub ||
		q.Get("privateKey") != priv || q.Get("ip") != "10.66.0.9/32" || q.Get("obfs") != "amneziawg" ||
		q.Get("udp") != "1" || q.Get("reserved") != "0,0,0" || pu.Fragment != model.ProtoAWG {
		t.Fatalf("link %s", l)
	}
	var obfs map[string]string
	if err := json.Unmarshal([]byte(q.Get("obfsParam")), &obfs); err != nil {
		t.Fatalf("obfsParam: %v", err)
	}
	p, _ := awg.FromModel(s.AWGParams)
	h1 := strconv.FormatUint(uint64(p.H1.Min), 10) + "-" + strconv.FormatUint(uint64(p.H1.Max), 10)
	if obfs["jc"] != strconv.Itoa(p.Jc) || obfs["s4"] != strconv.Itoa(p.S4) || obfs["h1"] != h1 ||
		obfs["header_protection_key"] != p.HeaderKey || obfs["i1"] != "" {
		t.Fatalf("obfsParam %v", obfs)
	}
	if p.Trailers && obfs["random_trailers"] != "true" {
		t.Fatalf("no random_trailers: %v", obfs)
	}
	if !p.Padding.IsZero() && !strings.Contains(obfs["content_padding_addition"], "-") {
		t.Fatalf("padding %v", obfs)
	}
	if AWGShadowrocketLink(u, s) != l {
		t.Fatal("the link changes between calls")
	}
	var got string
	for _, x := range ShareLinksFor(u, []Server{{Set: s, Access: model.UnrestrictedAccess()}}, "Shadowrocket/2.2.80 CFNetwork") {
		if strings.Contains(x, "obfs=amneziawg") {
			got = x
		}
	}
	if got == "" {
		t.Fatal("Shadowrocket got no AmneziaWG line")
	}
}
