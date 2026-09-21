package xray

import (
	"net/netip"
	"testing"
)

func TestAccessClient(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line string
		want string // "" = no connection to close
	}{
		{"2024/01/01 00:00:00.000000 from tcp:203.0.113.10:54321 accepted tcp:example.com:443 [vless-in >> direct] email: u1", "203.0.113.10:54321"},
		{"2024/01/01 00:00:00.000000 from 203.0.113.10:54321 accepted tcp:example.com:443 [vless-in >> direct] email: u1", "203.0.113.10:54321"},
		{"2024/01/01 00:00:00.000000 from tcp:[2001:db8::7]:4000 accepted tcp:example.com:443 [vless-in >> direct] email: u1", "[2001:db8::7]:4000"},
		// A dual-stack listener's IPv4 client, as the kernel will list it: unmapped.
		{"2024/01/01 00:00:00.000000 from tcp:[::ffff:198.51.100.2]:4000 accepted tcp:example.com:443 [vless-in >> direct] email: u1", "198.51.100.2:4000"},
		{"2024/01/01 00:00:00.000000 from udp:203.0.113.10:54321 accepted udp:1.1.1.1:53 [hysteria-in >> direct] email: u1", ""},
		{"2024/01/01 00:00:00.000000 from tcp:127.0.0.1:40000 accepted tcp:example.com:443 [vless-in >> direct] email: u1", ""},
		{"2024/01/01 00:00:00.000000 from tcp:203.0.113.10:0 accepted tcp:example.com:443 [custom-3 >> direct] email: u1", ""},
		{"no source here", ""},
	}
	for _, c := range cases {
		got, ok := accessClient(c.line)
		switch {
		case c.want == "" && ok:
			t.Errorf("%q: got %v, want no connection", c.line, got)
		case c.want != "" && (!ok || got != netip.MustParseAddrPort(c.want)):
			t.Errorf("%q: got %v %v, want %s", c.line, got, ok, c.want)
		}
	}
}

func TestAccessInbound(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"x from tcp:1.2.3.4:5 accepted tcp:example.com:443 [vless-in >> direct] email: u1":         "vless-in",
		"x from tcp:1.2.3.4:5 accepted tcp:example.com:443 [custom-2 -> warp] email: u1":           "custom-2",
		"x from tcp:1.2.3.4:5 accepted tcp:example.com:443 [vless-reality-in ==> block] email: u1": "vless-reality-in",
		// The destination's own brackets must not be taken for the route.
		"x from tcp:1.2.3.4:5 accepted tcp:[2001:db8::1]:443 [custom-9 >> direct] email: u1": "custom-9",
		"x from tcp:1.2.3.4:5 rejected something":                                            "",
	}
	for line, want := range cases {
		if got := accessInbound(line); got != want {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
}

func TestTCPUserPorts(t *testing.T) {
	t.Parallel()
	cfg := []byte(`{"inbounds":[
		{"tag":"api","port":10085,"protocol":"dokodemo-door"},
		{"tag":"vless-in","port":443,"protocol":"vless","streamSettings":{"network":"tcp","security":"tls"}},
		{"tag":"vless-reality-in","port":8443,"protocol":"vless","streamSettings":{"network":"xhttp","security":"reality"}},
		{"tag":"custom-1","port":2443,"protocol":"trojan","streamSettings":{"network":"raw","security":"tls"}},
		{"tag":"custom-2","port":"8388","protocol":"shadowsocks"},
		{"tag":"custom-4","port":2083,"protocol":"vless","streamSettings":{"network":"ws","security":"none"}},
		{"tag":"custom-5","port":2087,"protocol":"vless","streamSettings":{"network":"grpc","security":"tls"}},
		{"tag":"hysteria-in","port":443,"protocol":"hysteria"},
		{"tag":"custom-3","port":51820,"protocol":"wireguard"}
	]}`)
	got := tcpUserPorts(cfg)
	// WebSocket and gRPC may sit behind a CDN that carries many users on one connection:
	// not tracked. REALITY rules a CDN out, whatever the transport.
	want := map[string]uint16{"vless-in": 443, "vless-reality-in": 8443, "custom-1": 2443, "custom-2": 8388}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for tag, port := range want {
		if got[tag] != port {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestConnTrackerTakesOnlyTheRemovedInbounds: a user taken off one inbound keeps the
// connections they hold on another, and nobody else's are touched.
func TestConnTrackerTakesOnlyTheRemovedInbounds(t *testing.T) {
	t.Parallel()
	tr := newConnTracker(map[string]uint16{"vless-in": 443, "custom-1": 2443}, nil)
	tr.observe("u1", "x from tcp:203.0.113.1:1000 accepted tcp:a.com:443 [vless-in >> direct] email: u1")
	tr.observe("u1", "x from tcp:203.0.113.1:1001 accepted tcp:a.com:443 [custom-1 >> direct] email: u1")
	tr.observe("u1", "x from tcp:203.0.113.1:1001 accepted tcp:b.com:443 [custom-1 >> direct] email: u1") // same connection, second request
	tr.observe("u2", "x from tcp:203.0.113.2:2000 accepted tcp:a.com:443 [vless-in >> direct] email: u2")
	tr.observe("u3", "x from tcp:203.0.113.3:3000 accepted tcp:a.com:443 [system-socks-in >> direct] email: u3") // not a user inbound
	if tr.n != 3 {
		t.Fatalf("remembered %d connections, want 3", tr.n)
	}

	got := tr.take([]string{"u1"}, map[string]bool{"custom-1": true})
	want := connEnd{netip.MustParseAddrPort("203.0.113.1:1001"), 2443}
	if len(got) != 1 {
		t.Fatalf("taken %v, want only %v", got, want)
	}
	if _, ok := got[want]; !ok {
		t.Fatalf("taken %v, want %v", got, want)
	}

	got = tr.take([]string{"u1"}, nil)
	if _, ok := got[connEnd{netip.MustParseAddrPort("203.0.113.1:1000"), 443}]; !ok || len(got) != 1 {
		t.Fatalf("second take = %v, want u1's vless-in connection", got)
	}
	if tr.n != 1 || len(tr.users["u2"]) != 1 {
		t.Fatalf("after taking u1: n=%d, u2=%v — u2 must be left alone", tr.n, tr.users["u2"])
	}
}

// A client address another user comes in from next is theirs: removing the first user
// must not close it.
func TestConnTrackerAddressChangesHands(t *testing.T) {
	t.Parallel()
	tr := newConnTracker(map[string]uint16{"vless-in": 443}, nil)
	line := func(email string) string {
		return "x from tcp:198.51.100.9:40000 accepted tcp:a.com:443 [vless-in >> direct] email: " + email
	}
	tr.observe("u1", line("u1"))
	tr.observe("u2", line("u2"))
	if got := tr.take([]string{"u1"}, nil); len(got) != 0 {
		t.Fatalf("removing u1 took %v, which u2 holds now", got)
	}
	if got := tr.take([]string{"u2"}, nil); len(got) != 1 || tr.n != 0 {
		t.Fatalf("removing u2 took %v (n=%d), want its one connection", got, tr.n)
	}
}

// The front's port is what a fronted lane's connections are closed on, and a config
// applied live does not put the listening port back in its place.
func TestConnTrackerPublicPortSurvivesNewPorts(t *testing.T) {
	t.Parallel()
	tr := newConnTracker(map[string]uint16{"vless-in": 18443}, map[string]uint16{"vless-in": 443})
	tr.setPorts(map[string]uint16{"vless-in": 18443, "custom-2": 2443})
	tr.observe("u1", "x from tcp:203.0.113.1:1000 accepted tcp:a.com:443 [vless-in >> direct] email: u1")
	tr.observe("u1", "x from tcp:203.0.113.1:1001 accepted tcp:a.com:443 [custom-2 >> direct] email: u1")
	got := tr.take([]string{"u1"}, nil)
	for _, want := range []connEnd{
		{netip.MustParseAddrPort("203.0.113.1:1000"), 443},
		{netip.MustParseAddrPort("203.0.113.1:1001"), 2443},
	} {
		if _, ok := got[want]; !ok {
			t.Fatalf("took %v, want %v among them", got, want)
		}
	}
}
