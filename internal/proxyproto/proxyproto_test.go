package proxyproto

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// loopbackPair dials the wrapped listener over 127.0.0.1 and hands back the
// server side (wrapped) and the raw client side. Loopback is the only peer the
// wrapper trusts, so this is the setup every header case needs.
func loopbackPair(t *testing.T) (server, client net.Conn) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := &Listener{Listener: raw}
	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 1)
	errc := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			errc <- err
			return
		}
		accepted <- c
	}()
	client, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	select {
	case server = <-accepted:
	case err := <-errc:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("accept timed out")
	}
	t.Cleanup(func() { _ = server.Close() })
	_ = server.SetDeadline(time.Now().Add(2 * time.Second))
	return server, client
}

func readN(t *testing.T, c net.Conn, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(buf)
}

func TestValidHeaderSetsRemoteAddrAndIsStripped(t *testing.T) {
	server, client := loopbackPair(t)
	go func() { _, _ = client.Write([]byte("PROXY TCP4 203.0.113.9 127.0.0.1 56324 443\r\nhello")) }()

	// net/http asks for RemoteAddr before it ever reads, so the address must be
	// right on its own — not only after a Read has run the parser.
	if got := server.RemoteAddr().String(); got != "203.0.113.9:56324" {
		t.Errorf("RemoteAddr = %q, want the client from the header", got)
	}
	// The header is consumed: the application sees the request bytes only.
	if got := readN(t, server, 5); got != "hello" {
		t.Errorf("payload = %q, want the header stripped", got)
	}
}

func TestIPv6HeaderIsHonoured(t *testing.T) {
	server, client := loopbackPair(t)
	go func() { _, _ = client.Write([]byte("PROXY TCP6 2001:db8::7 ::1 4000 443\r\nx")) }()
	if got := server.RemoteAddr().String(); got != "[2001:db8::7]:4000" {
		t.Errorf("RemoteAddr = %q, want the IPv6 client", got)
	}
}

// A connection that does not begin with "PROXY " is exactly what a direct
// (non-Xray) peer sends; its bytes must reach the application untouched and its
// transport address must be the one reported.
func TestNoHeaderPassesThroughUnchanged(t *testing.T) {
	server, client := loopbackPair(t)
	go func() { _, _ = client.Write([]byte("GET / HTTP/1.1\r\n")) }()

	if got := server.RemoteAddr().String(); got != client.LocalAddr().String() {
		t.Errorf("RemoteAddr = %q, want the transport address %q", got, client.LocalAddr())
	}
	if got := readN(t, server, 16); got != "GET / HTTP/1.1\r\n" {
		t.Errorf("payload = %q; the peeked bytes were lost", got)
	}
}

// A line that starts like a header but cannot be parsed (Xray emits "PROXY
// UNKNOWN" when it has no client address) is consumed — it is not part of the
// request — and the transport address stays in force.
func TestMalformedHeaderFallsBackToTransportAddress(t *testing.T) {
	server, client := loopbackPair(t)
	go func() { _, _ = client.Write([]byte("PROXY UNKNOWN\r\nbody")) }()

	if got := server.RemoteAddr().String(); got != client.LocalAddr().String() {
		t.Errorf("RemoteAddr = %q, want the transport address", got)
	}
	if got := readN(t, server, 4); got != "body" {
		t.Errorf("payload = %q, want the unparseable header line consumed", got)
	}
}

// fakePeer is a net.Conn whose remote address is whatever the test says.
type fakePeer struct {
	net.Conn
	remote net.Addr
}

func (f fakePeer) RemoteAddr() net.Addr { return f.remote }

// The whole point of the loopback rule: a peer that is not Xray on 127.0.0.1
// must not be able to pick its own source IP, or it could dodge the per-IP login
// throttle. Its header bytes are left in the stream rather than consumed, so the
// spoof attempt is visible as a garbage request instead of vanishing.
func TestNonLoopbackPeerCannotSpoofViaHeader(t *testing.T) {
	serverEnd, clientEnd := net.Pipe()
	defer serverEnd.Close()
	defer clientEnd.Close()
	public := &net.TCPAddr{IP: net.ParseIP("198.51.100.4"), Port: 5555}
	c := &conn{Conn: fakePeer{Conn: serverEnd, remote: public}, r: bufio.NewReader(serverEnd)}

	header := "PROXY TCP4 203.0.113.9 127.0.0.1 56324 443\r\n"
	go func() { _, _ = clientEnd.Write([]byte(header)) }()

	if got := c.RemoteAddr(); got.String() != public.String() {
		t.Fatalf("RemoteAddr = %v, a non-loopback peer spoofed its address", got)
	}
	if got := readN(t, c, len(header)); got != header {
		t.Errorf("read %q, want the header left in the stream for a non-loopback peer", got)
	}
}

func TestParseV1(t *testing.T) {
	cases := []struct {
		line string
		want string // "" means nil
	}{
		{"PROXY TCP4 1.2.3.4 5.6.7.8 56324 443\r\n", "1.2.3.4:56324"},
		{"PROXY TCP6 2001:db8::1 ::1 1 2", "[2001:db8::1]:1"},
		{"PROXY UNKNOWN\r\n", ""},
		{"PROXY TCP4 1.2.3.4 5.6.7.8 56324", ""},   // too few fields
		{"PROXY TCP4 not-an-ip 5.6.7.8 1 2", ""},   // bad source
		{"PROXY TCP4 1.2.3.4 5.6.7.8 abc 443", ""}, // bad port
		{"PROXX TCP4 1.2.3.4 5.6.7.8 1 2", ""},     // wrong signature
		{"", ""},
	}
	for _, tc := range cases {
		got := parseV1(tc.line)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("parseV1(%q) = %v, want nil", tc.line, got)
		case tc.want != "" && (got == nil || got.String() != tc.want):
			t.Errorf("parseV1(%q) = %v, want %s", tc.line, got, tc.want)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	tcp := func(s string) net.Addr {
		a, err := net.ResolveTCPAddr("tcp", s)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	if !isLoopback(tcp("127.0.0.1:80")) || !isLoopback(tcp("127.9.9.9:80")) || !isLoopback(tcp("[::1]:80")) {
		t.Error("loopback addresses were not recognised")
	}
	if isLoopback(tcp("10.0.0.1:80")) || isLoopback(tcp("[2001:db8::1]:80")) {
		t.Error("a routable address counted as loopback")
	}
	// net.Pipe addresses and a missing address are not loopback either: only a
	// real 127/8 or ::1 peer may carry a header.
	a, _ := net.Pipe()
	defer a.Close()
	if isLoopback(a.RemoteAddr()) || isLoopback(nil) {
		t.Error("a non-IP address counted as loopback")
	}
	if !strings.HasPrefix(a.RemoteAddr().String(), "pipe") {
		t.Skip("net.Pipe address format changed; the check above may not be meaningful")
	}
}
