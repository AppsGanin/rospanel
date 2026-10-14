package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestHostNameHealth(t *testing.T) {
	for _, h := range []string{"", "144.31.159.81", "2001:db8::1", "cdn.example.com", "files.mysite.net"} {
		if c := hostNameHealth(h); c != nil {
			t.Errorf("%q flagged: %+v", h, c)
		}
	}
	for host, word := range map[string]string{"vpn.example.com": "vpn", "My-Proxy.io": "proxy", "x.panel-ru.net": "panel"} {
		c := hostNameHealth(host)
		if c == nil || c.Status != healthWarn || c.Args["word"] != word {
			t.Errorf("%q: %+v, want word %q", host, c, word)
		}
	}
}

func TestPickRealityPortIsFreeAndOffTheDefault(t *testing.T) {
	for range 5 {
		p := PickRealityPort()
		if p == model.DefaultRealityPort || p < 20000 || p >= 60000 {
			t.Fatalf("port %d", p)
		}
	}
}
