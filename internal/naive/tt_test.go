package naive

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

var ttAuth = model.TrustTunnelLogin(testEmail) + ":" + testUUID

func ttLane(t *testing.T) (*fakeXray, *Server, string) {
	fx := newFakeXray(t)
	s := NewTrustTunnel(fx.ln.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	return fx, s, startLane(t, s)
}

// ttPacket is a client UDP packet as the TrustTunnel client composes it.
func ttPacket(src, dst netip.AddrPort, app, payload string) []byte {
	b := make([]byte, 4+ttHeader)
	putTTAddr(b[4:], src)
	putTTAddr(b[4+ttAddrLen+2:], dst)
	b[4+ttHeader-1] = byte(len(app))
	b = append(append(b, app...), payload...)
	binary.BigEndian.PutUint32(b, uint32(len(b)-4))
	return b
}

// ttReply is what the endpoint sends back for a packet of src→dst.
func ttReply(src, dst netip.AddrPort, payload string) []byte {
	b := make([]byte, 4+2*(ttAddrLen+2))
	putTTAddr(b[4:], dst)
	putTTAddr(b[4+ttAddrLen+2:], src)
	b = append(b, payload...)
	binary.BigEndian.PutUint32(b, uint32(len(b)-4))
	return b
}

func TestTTTarget(t *testing.T) {
	for authority, want := range map[string]ttKind{
		"_check": ttCheck, "_check:0": ttCheck, "_udp2": ttUDP, "_udp2:0": ttUDP,
		"_icmp": ttICMP, "_icmp:0": ttICMP, "example.com:443": ttTCP, "[2001:db8::1]:80": ttTCP,
	} {
		_, _, kind, err := ttTarget(authority)
		if err != nil || kind != want {
			t.Errorf("%q: kind %v err %v, want %v", authority, kind, err, want)
		}
	}
	for _, bad := range []string{"_udp2:53", "example.com", "example.com:0", "_other:443"} {
		if _, _, _, err := ttTarget(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTTAddrRoundTrips(t *testing.T) {
	for _, s := range []string{"1.2.3.4:53", "[2001:db8::1]:443", "[::1]:9"} {
		ap := netip.MustParseAddrPort(s)
		b := make([]byte, ttAddrLen+2)
		putTTAddr(b, ap)
		if got := ttAddr(b); got != ap && s != "[::1]:9" {
			t.Errorf("%s: read back %s", s, got)
		}
	}
	b := make([]byte, ttAddrLen+2)
	putTTAddr(b, netip.MustParseAddrPort("1.2.3.4:53"))
	if !bytes.Equal(b[:12], make([]byte, 12)) || !bytes.Equal(b[12:16], []byte{1, 2, 3, 4}) {
		t.Fatalf("IPv4 not zero-padded: % x", b)
	}
}

// The health check is a 200 that ends the stream, and reaches no inbound.
func TestTTHealthCheck(t *testing.T) {
	fx, _, addr := ttLane(t)
	resp := connect(t, addr, ttAuth, "_check", http.NoBody, nil)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || err != nil || len(body) != 0 {
		t.Fatalf("check: %d %q %v", resp.StatusCode, body, err)
	}
	if resp.Header.Get("Padding") != "" {
		t.Fatalf("NaiveProxy padding header on TrustTunnel: %v", resp.Header)
	}
	if len(fx.targets) != 0 {
		t.Fatalf("health check dialed Xray: %v", fx.targets)
	}
}

func TestTTTCPTunnel(t *testing.T) {
	fx, _, addr := ttLane(t)
	pr, pw := io.Pipe()
	resp := connect(t, addr, ttAuth, "example.com:443", pr, map[string]string{"User-Agent": "linux trusttunnel"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	go func() { _, _ = pw.Write([]byte("hello")) }()
	if got := readFull(t, resp.Body, 5); string(got) != "hello" {
		t.Fatalf("echo %q", got)
	}
	pw.Close()
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if !slices.Equal(fx.targets, []string{"example.com:443"}) {
		t.Fatalf("targets %v", fx.targets)
	}
}

// Each lane takes only its own login: the NaiveProxy credential does not open
// TrustTunnel, nor the other way round, and both fall through to the decoy.
func TestTTAndNaiveLoginsStayApart(t *testing.T) {
	fx := newFakeXray(t)
	nv := New(fx.ln.Addr().String())
	tt := NewTrustTunnel(fx.ln.Addr().String())
	nv.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	tt.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	for name, c := range map[string]struct {
		s    *Server
		auth string
	}{
		"naive login on TrustTunnel":  {tt, testEmail + ":" + testUUID},
		"TrustTunnel login on naive":  {nv, ttAuth},
		"health check without a user": {tt, "tt-u8:" + testUUID},
	} {
		addr := startLane(t, c.s)
		target := "example.com:443"
		if c.s == tt {
			target = "_check"
		}
		resp := connect(t, addr, c.auth, target, http.NoBody, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || resp.Header.Get("X-Decoy") != "1" {
			t.Errorf("%s: %d — want the decoy", name, resp.StatusCode)
		}
	}
}

// _udp2 carries every flow of the session: one VLESS UDP connection per 4-tuple, each
// reply addressed back to the socket it answers, the app name dropped.
func TestTTUDP(t *testing.T) {
	fx, _, addr := ttLane(t)
	pr, pw := io.Pipe()
	resp := connect(t, addr, ttAuth, "_udp2", pr, map[string]string{"User-Agent": "linux _udp2"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	src1 := netip.MustParseAddrPort("172.16.219.2:50000")
	src2 := netip.MustParseAddrPort("172.16.219.2:50001")
	dns := netip.MustParseAddrPort("8.8.8.8:53")
	v6 := netip.MustParseAddrPort("[2001:db8::1]:443")
	for i, c := range []struct {
		src, dst netip.AddrPort
		app      string
	}{{src1, dns, "com.example"}, {src2, dns, ""}, {src1, v6, "browser"}, {src1, dns, "com.example"}} {
		payload := string(rune('A'+i)) + "-payload"
		if _, err := pw.Write(ttPacket(c.src, c.dst, c.app, payload)); err != nil {
			t.Fatal(err)
		}
		want := ttReply(c.src, c.dst, payload)
		if got := readFull(t, resp.Body, len(want)); !bytes.Equal(got, want) {
			t.Fatalf("reply %d: % x, want % x", i, got, want)
		}
	}
	// A packet too short for its header is skipped, not fatal.
	if _, err := pw.Write([]byte{0, 0, 0, 3, 1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write(ttPacket(src2, dns, "", "after")); err != nil {
		t.Fatal(err)
	}
	want := ttReply(src2, dns, "after")
	if got := readFull(t, resp.Body, len(want)); !bytes.Equal(got, want) {
		t.Fatalf("reply after a short packet: % x", got)
	}
	pw.Close()
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if want := []string{"udp:8.8.8.8:53", "udp:8.8.8.8:53", "udp:[2001:db8::1]:443"}; !slices.Equal(fx.targets, want) {
		t.Fatalf("targets %v, want %v (one per 4-tuple)", fx.targets, want)
	}
}

// _icmp is accepted and its requests dropped.
func TestTTICMPIsAcceptedAndDropped(t *testing.T) {
	fx, _, addr := ttLane(t)
	pr, pw := io.Pipe()
	resp := connect(t, addr, ttAuth, "_icmp", pr, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if _, err := pw.Write(make([]byte, 3*ttICMPRequest)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	if b, err := io.ReadAll(resp.Body); err != nil || len(b) != 0 {
		t.Fatalf("icmp stream answered %q %v", b, err)
	}
	if len(fx.targets) != 0 {
		t.Fatalf("icmp dialed Xray: %v", fx.targets)
	}
}

// A user removed from trusttunnel-in loses the _udp2 stream they hold, with a reset.
func TestTTUDPCut(t *testing.T) {
	_, s, addr := ttLane(t)
	pr, pw := io.Pipe()
	defer pw.Close()
	resp := connect(t, addr, ttAuth, "_udp2", pr, nil) //nolint:bodyclose // closed by the defer; read in a goroutine below
	defer resp.Body.Close()
	src, dst := netip.MustParseAddrPort("10.0.0.2:5000"), netip.MustParseAddrPort("9.9.9.9:53")
	if _, err := pw.Write(ttPacket(src, dst, "", "q")); err != nil {
		t.Fatal(err)
	}
	readFull(t, resp.Body, len(ttReply(src, dst, "q")))
	s.SetUsers("", nil)
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, resp.Body); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the cut stream ended cleanly; want a reset")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the removed user's _udp2 stream stayed open")
	}
}

// Hostile framing: an app name longer than the packet and a payload past what VLESS can
// frame are skipped without desynchronising the stream; a length past any packet ends it.
func TestTTUDPMalformed(t *testing.T) {
	fx, _, addr := ttLane(t)
	pr, pw := io.Pipe()
	defer pw.Close()
	resp := connect(t, addr, ttAuth, "_udp2", pr, nil) //nolint:bodyclose // closed by the defer; read in a goroutine below
	defer resp.Body.Close()
	src, dst := netip.MustParseAddrPort("10.0.0.2:5000"), netip.MustParseAddrPort("9.9.9.9:53")

	bad := ttPacket(src, dst, "", "xy")
	// An app name longer than the 2 bytes that follow, and a payload one past a frame.
	bad[4+ttHeader-1] = 200
	huge := ttPacket(src, dst, "", strings.Repeat("z", 0xffff+1))
	ok := ttPacket(src, dst, "", "fine")
	for _, p := range [][]byte{bad, huge, ok} {
		if _, err := pw.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	want := ttReply(src, dst, "fine")
	if got := readFull(t, resp.Body, len(want)); !bytes.Equal(got, want) {
		t.Fatalf("after malformed packets: % x", got)
	}
	fx.mu.Lock()
	n := len(fx.targets)
	fx.mu.Unlock()
	if n != 1 {
		t.Fatalf("malformed packets dialed Xray: %d dials", n)
	}

	var over [4]byte
	binary.BigEndian.PutUint32(over[:], ttMaxPacket+1)
	if _, err := pw.Write(over[:]); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an impossible length left the stream open")
	}
}

// Past the per-stream bound the flow idle longest makes room, and its connection closes.
func TestTTUDPEvictsTheIdlest(t *testing.T) {
	fx, _, addr := ttLane(t)
	pr, pw := io.Pipe()
	defer pw.Close()
	resp := connect(t, addr, ttAuth, "_udp2", pr, nil)
	defer resp.Body.Close()
	dst := netip.MustParseAddrPort("9.9.9.9:53")
	for i := range ttMaxUDPDests + 1 {
		src := netip.AddrPortFrom(netip.MustParseAddr("10.0.0.2"), uint16(1000+i))
		if _, err := pw.Write(ttPacket(src, dst, "", "x")); err != nil {
			t.Fatal(err)
		}
		readFull(t, resp.Body, len(ttReply(src, dst, "x")))
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		fx.mu.Lock()
		open := 0
		for _, c := range fx.conns {
			if c.SetReadDeadline(time.Time{}) == nil {
				open++
			}
		}
		fx.mu.Unlock()
		if open <= ttMaxUDPDests || time.Now().After(deadline) {
			if open > ttMaxUDPDests {
				t.Fatalf("%d connections open, bound is %d", open, ttMaxUDPDests)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
