package sub

import (
	"encoding/base64"
	"net/url"
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
