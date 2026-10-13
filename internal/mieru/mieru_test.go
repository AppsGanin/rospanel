package mieru

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mieruclient "github.com/enfein/mieru/v3/apis/client"
	apicommon "github.com/enfein/mieru/v3/apis/common"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"google.golang.org/protobuf/proto"

	"github.com/AppsGanin/rospanel/internal/vlessin"
)

const (
	testEmail = "u7"
	testUUID  = "11111111-2222-3333-4444-555555555555"
	otherUUID = "66666666-7777-8888-9999-000000000000"
)

// fakeXray stands in for mieru-in: it reads the PROXY line and the VLESS request, checks
// the user and echoes — TCP bytes, or VLESS UDP packets as they came.
type fakeXray struct {
	ln net.Listener

	mu      sync.Mutex
	proxies []string
	targets []string
}

func newFakeXray(t *testing.T) *fakeXray {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeXray{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeXray) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	hdr := make([]byte, 1+16+1+1+2+1)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	known := false
	for _, s := range []string{testUUID, otherUUID} {
		id, _ := vlessin.ParseUUID(s)
		known = known || bytes.Equal(hdr[1:17], id[:])
	}
	if hdr[0] != 0 || !known {
		return
	}
	var host string
	switch hdr[21] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 2:
		n, _ := br.ReadByte()
		b := make([]byte, n)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = string(b)
	case 3:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	}
	tgt := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(hdr[19:21]))))
	if hdr[18] == vlessin.CmdUDP {
		tgt = "udp:" + tgt
	}
	f.mu.Lock()
	f.proxies = append(f.proxies, line)
	f.targets = append(f.targets, tgt)
	f.mu.Unlock()
	if _, err := c.Write([]byte{0, 0}); err != nil {
		return
	}
	_, _ = io.Copy(c, br) // echo; VLESS UDP framing echoes as it is
}

func (f *fakeXray) seen() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.proxies...), append([]string(nil), f.targets...)
}

func freePort(t *testing.T, transport string) int {
	t.Helper()
	if transport == UDP {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		return pc.LocalAddr().(*net.UDPAddr).Port
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// startLane runs the lane on a free port with the test user.
func startLane(t *testing.T, transport string) (*Server, *fakeXray, Listen) {
	t.Helper()
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	l := Listen{Port: freePort(t, transport), Transport: transport}
	if err := s.Apply(l, "", []User{{Email: testEmail, UUID: testUUID}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, fx, l
}

// client is the official mieru client, on the given credentials.
func client(t *testing.T, l Listen, name, pass string) mieruclient.Client {
	t.Helper()
	tp := appctlpb.TransportProtocol_TCP
	if l.Transport == UDP {
		tp = appctlpb.TransportProtocol_UDP
	}
	c := mieruclient.NewClient()
	err := c.Store(&mieruclient.ClientConfig{Profile: &appctlpb.ClientProfile{
		ProfileName: proto.String("test"),
		User:        &appctlpb.User{Name: proto.String(name), Password: proto.String(pass)},
		Servers: []*appctlpb.ServerEndpoint{{
			IpAddress:    proto.String("127.0.0.1"),
			PortBindings: []*appctlpb.PortBinding{{Port: proto.Int32(int32(l.Port)), Protocol: tp.Enum()}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop() })
	return c
}

func dial(t *testing.T, c mieruclient.Client, network, addr string) (net.Conn, error) {
	t.Helper()
	return dialWithin(t, c, network, addr, 5*time.Second)
}

func dialWithin(t *testing.T, c mieruclient.Client, network, addr string, d time.Duration) (net.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var a net.Addr
	var err error
	if network == "udp" {
		a, err = net.ResolveUDPAddr("udp", addr)
	} else {
		a, err = net.ResolveTCPAddr("tcp", addr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return c.DialContext(ctx, a)
}

func echoes(t *testing.T, conn net.Conn, msg []byte) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("echo: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("echoed wrong")
	}
	_ = conn.SetDeadline(time.Time{})
}

func TestTCPTunnel(t *testing.T) {
	for _, transport := range []string{TCP, UDP} {
		t.Run(transport, func(t *testing.T) {
			_, fx, l := startLane(t, transport)
			c := client(t, l, testEmail, testUUID)
			conn, err := dial(t, c, "tcp", "93.184.216.34:443")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			echoes(t, conn, bytes.Repeat([]byte("mieru "), 50000))
			proxies, targets := fx.seen()
			if len(targets) != 1 || targets[0] != "93.184.216.34:443" {
				t.Fatalf("targets %v", targets)
			}
			if !strings.HasPrefix(proxies[0], "PROXY TCP4 127.0.0.1 127.0.0.1 ") {
				t.Fatalf("proxy line %q", proxies[0])
			}
		})
	}
}

func TestUDPTunnel(t *testing.T) {
	for _, transport := range []string{TCP, UDP} {
		t.Run(transport, func(t *testing.T) {
			_, fx, l := startLane(t, transport)
			c := client(t, l, testEmail, testUUID)
			conn, err := dial(t, c, "udp", "0.0.0.0:0")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			pc := apicommon.NewPacketOverStreamTunnel(conn)
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			for i, dst := range [][]byte{
				{1, 1, 1, 1, 1, 0, 53},                       // 1.1.1.1:53
				{3, 6, 'a', '.', 'b', '.', 'c', 'd', 0, 123}, // a.b.cd:123
				{1, 1, 1, 1, 1, 0, 53},                       // the same destination again: the same connection
			} {
				payload := []byte("query-" + strconv.Itoa(i))
				dgram := append(append([]byte{0, 0, 0}, dst...), payload...)
				if _, err := pc.Write(dgram); err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, 2048)
				n, err := pc.Read(buf)
				if err != nil {
					t.Fatalf("reply %d: %v", i, err)
				}
				if !bytes.Equal(buf[:n], dgram) {
					t.Fatalf("reply %d: % x, want % x", i, buf[:n], dgram)
				}
			}
			if _, targets := fx.seen(); len(targets) != 2 || targets[0] != "udp:1.1.1.1:53" || targets[1] != "udp:a.b.cd:123" {
				t.Fatalf("targets %v", targets)
			}
		})
	}
}

func TestWrongPasswordGetsNothing(t *testing.T) {
	t.Parallel() // the client takes ~11 s to give up on a server that never answers
	_, fx, l := startLane(t, TCP)
	c := client(t, l, testEmail, otherUUID)
	conn, err := dialWithin(t, c, "tcp", "93.184.216.34:443", time.Second)
	if err == nil {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Write([]byte("x"))
		_, err = conn.Read(make([]byte, 1))
		conn.Close()
	}
	if err == nil {
		t.Fatal("a wrong password got through")
	}
	if _, targets := fx.seen(); len(targets) != 0 {
		t.Fatalf("Xray was dialed: %v", targets)
	}
}

// Users change without the listener restarting: a new one gets in, a removed one's open
// session ends and they get no new one, and nobody else is touched.
func TestApplyChangesUsersLive(t *testing.T) {
	s, _, l := startLane(t, TCP)
	stay := client(t, l, testEmail, testUUID)
	kept, err := dial(t, stay, "tcp", "10.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	echoes(t, kept, []byte("before"))

	if err := s.Apply(l, "", []User{{Email: testEmail, UUID: testUUID}, {Email: "u8", UUID: otherUUID}}); err != nil {
		t.Fatal(err)
	}
	added := client(t, l, "u8", otherUUID)
	conn, err := dial(t, added, "tcp", "10.0.0.2:80")
	if err != nil {
		t.Fatal(err)
	}
	echoes(t, conn, []byte("added"))
	echoes(t, kept, []byte("still here"))

	// u8 removed: its open session ends at once.
	if err := s.Apply(l, "", []User{{Email: testEmail, UUID: testUUID}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	if _, err := io.Copy(io.Discard, conn); err != nil && time.Since(start) > 2*time.Second {
		t.Fatalf("the removed user's session was not ended: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the removed user's session outlived the change")
	}
	conn.Close()
	echoes(t, kept, []byte("untouched"))
	if s.Listening() != l {
		t.Fatalf("listener moved: %+v", s.Listening())
	}
}

func TestPortChangeMovesTheListener(t *testing.T) {
	s, _, l := startLane(t, TCP)
	moved := Listen{Port: freePort(t, TCP), Transport: TCP}
	if err := s.Apply(moved, "", []User{{Email: testEmail, UUID: testUUID}}); err != nil {
		t.Fatal(err)
	}
	if s.Listening() != moved {
		t.Fatalf("listening %+v, want %+v", s.Listening(), moved)
	}
	conn, err := dial(t, client(t, moved, testEmail, testUUID), "tcp", "10.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echoes(t, conn, []byte("moved"))
	if ln, err := net.Listen("tcp", ":"+strconv.Itoa(l.Port)); err != nil {
		t.Fatalf("the old port is still held: %v", err)
	} else {
		ln.Close()
	}

	// Off: nothing listens.
	if err := s.Apply(Listen{}, "", nil); err != nil {
		t.Fatal(err)
	}
	if ln, err := net.Listen("tcp", ":"+strconv.Itoa(moved.Port)); err != nil {
		t.Fatalf("still listening after switching off: %v", err)
	} else {
		ln.Close()
	}
}

// With no users the lane still runs (mieru refuses an empty table) and lets nobody in.
func TestNoUsers(t *testing.T) {
	t.Parallel()
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	l := Listen{Port: freePort(t, TCP), Transport: TCP}
	if err := s.Apply(l, "", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if s.Users() != 0 {
		t.Fatalf("users %d", s.Users())
	}
	c := client(t, l, testEmail, testUUID)
	if conn, err := dialWithin(t, c, "tcp", "10.0.0.1:80", time.Second); err == nil {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Write([]byte("x"))
		if _, err := conn.Read(make([]byte, 1)); err == nil {
			t.Fatal("a user got in to an empty lane")
		}
		conn.Close()
	}
}

func TestParseDatagram(t *testing.T) {
	for _, tc := range []struct {
		in         []byte
		host       string
		port       uint16
		payload    string
		ok         bool
		rawAddrLen int
	}{
		{[]byte{0, 0, 0, 1, 8, 8, 8, 8, 0, 53, 'q'}, "8.8.8.8", 53, "q", true, 7},
		{append(append([]byte{0, 0, 0, 4}, net.ParseIP("2001:db8::1").To16()...), 1, 0, 'x'), "2001:db8::1", 256, "x", true, 19},
		{[]byte{0, 0, 0, 3, 3, 'a', '.', 'b', 0, 80}, "a.b", 80, "", true, 7},
		{[]byte{0, 0, 1, 1, 8, 8, 8, 8, 0, 53}, "", 0, "", false, 0}, // a fragment
		{[]byte{0, 0, 0, 1, 8, 8}, "", 0, "", false, 0},              // short
		{[]byte{0, 0, 0, 3, 0, 0, 80}, "", 0, "", false, 0},          // empty name
		{[]byte{0, 0, 0, 1, 8, 8, 8, 8, 0, 0}, "", 0, "", false, 0},  // port 0
		{[]byte{0, 0, 0, 9, 1}, "", 0, "", false, 0},                 // unknown type
	} {
		raw, host, port, payload, ok := parseDatagram(tc.in)
		if ok != tc.ok || host != tc.host || port != tc.port || string(payload) != tc.payload || len(raw) != tc.rawAddrLen {
			t.Errorf("% x: %q %d %q %v (raw %d)", tc.in, host, port, payload, ok, len(raw))
		}
	}
}

// A listener that could not start (its port taken) starts on Retry once the port is free.
func TestRetryAfterThePortFrees(t *testing.T) {
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	t.Cleanup(s.Close)
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	l := Listen{Port: busy.Addr().(*net.TCPAddr).Port, Transport: TCP}
	if err := s.Apply(l, "", []User{{Email: testEmail, UUID: testUUID}}); err == nil {
		t.Fatal("started on a taken port")
	}
	if err := s.Retry(); err == nil {
		t.Fatal("retry started on a taken port")
	}
	busy.Close()
	if err := s.Retry(); err != nil {
		t.Fatal(err)
	}
	if s.Listening() != l {
		t.Fatalf("listening %+v, want %+v", s.Listening(), l)
	}
	conn, err := dial(t, client(t, l, testEmail, testUUID), "tcp", "10.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echoes(t, conn, []byte("retried"))
}

// A new password for a user ends the connection a client made with the old one: mieru
// checks the password once per connection, so a client still on it would otherwise
// keep opening sessions.
func TestNewPasswordShutsTheOldOneOut(t *testing.T) {
	t.Parallel()
	s, _, l := startLane(t, TCP)
	old := client(t, l, testEmail, testUUID)
	conn, err := dial(t, old, "tcp", "10.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	echoes(t, conn, []byte("before"))
	conn.Close()
	if err := s.Apply(l, "", []User{{Email: testEmail, UUID: otherUUID}}); err != nil {
		t.Fatal(err)
	}
	if again, err := dialWithin(t, old, "tcp", "10.0.0.1:80", time.Second); err == nil {
		_ = again.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = again.Write([]byte("x"))
		if _, err := again.Read(make([]byte, 1)); err == nil {
			t.Fatal("the old password still opens sessions")
		}
		again.Close()
	}
	fresh, err := dial(t, client(t, l, testEmail, otherUUID), "tcp", "10.0.0.2:80")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	echoes(t, fresh, []byte("new password"))
}

// The same through a gap: the user is removed, gets a new password while gone, and comes
// back — the old client's connection must not carry them back in on the old one.
func TestNewPasswordAfterAGap(t *testing.T) {
	t.Parallel()
	s, _, l := startLane(t, TCP)
	old := client(t, l, testEmail, testUUID)
	conn, err := dial(t, old, "tcp", "10.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	echoes(t, conn, []byte("before"))
	conn.Close()
	if err := s.Apply(l, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(l, "", []User{{Email: testEmail, UUID: otherUUID}}); err != nil {
		t.Fatal(err)
	}
	if again, err := dialWithin(t, old, "tcp", "10.0.0.1:80", time.Second); err == nil {
		_ = again.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = again.Write([]byte("x"))
		if _, err := again.Read(make([]byte, 1)); err == nil {
			t.Fatal("the old password got back in after a gap")
		}
		again.Close()
	}
}
