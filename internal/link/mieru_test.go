package link

import (
	"net/url"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The link reads back the way mieru's own parser reads it: user, password, host, one
// port paired with one protocol, a profile.
func TestMieruLink(t *testing.T) {
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}
	for host, wantHost := range map[string]string{"vpn.example.com": "vpn.example.com", "2001:db8::1": "2001:db8::1", "203.0.113.5": "203.0.113.5"} {
		set := &model.Settings{Host: host, MieruEnabled: true, MieruPort: 34567, MieruTransport: model.MieruUDP} // served as TCP
		if !MieruOn(set) {
			t.Fatal("lane off")
		}
		l, err := url.Parse(Mieru(u, set))
		if err != nil {
			t.Fatal(err)
		}
		pw, _ := l.User.Password()
		q := l.Query()
		if l.Scheme != "mierus" || l.User.Username() != "u7" || pw != u.UUID || l.Hostname() != wantHost ||
			q.Get("port") != "34567" || q.Get("protocol") != "TCP" || q.Get("profile") != model.ProtoMieru ||
			l.Fragment != model.ProtoMieru || len(q["port"]) != 1 || len(q["protocol"]) != 1 || l.Port() != "34567" {
			t.Fatalf("%s: %s", host, l)
		}
	}
	if MieruOn(&model.Settings{MieruEnabled: true}) {
		t.Fatal("a lane with no port is on")
	}
}

// A name with spaces travels as %20 in profile: Shadowrocket names the entry from it and
// does not read "+" as a space.
func TestMieruLinkNameWithSpaces(t *testing.T) {
	set := &model.Settings{Host: "vpn.example.com", MieruEnabled: true, MieruPort: 34567, NodeLabel: "NL"}
	l := Mieru(model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}, set)
	if strings.Contains(l, "+") {
		t.Fatalf("a + in %s", l)
	}
	pu, err := url.Parse(l)
	if err != nil {
		t.Fatal(err)
	}
	if got := pu.Query().Get("profile"); got != LabelFor(model.ProtoMieru, model.User{ID: 7}, set) || !strings.Contains(got, " ") {
		t.Fatalf("profile %q", got)
	}
}
