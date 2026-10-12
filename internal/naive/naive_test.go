package naive

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/proxyproto"
)

const (
	testUUID  = "5783a3e7-e373-51cd-8642-c83782b807c5"
	testEmail = "u7"
)

// padClient frames data the way a NaiveProxy client pads its first writes.
func padClient(data []byte, pad int) []byte {
	out := binary.BigEndian.AppendUint16(nil, uint16(len(data)))
	out = append(out, byte(pad))
	out = append(out, data...)
	return append(out, make([]byte, pad)...)
}

func TestUnpadReaderStripsTheFirstFrames(t *testing.T) {
	var wire, want []byte
	for i := range paddedFrames + 3 {
		chunk := bytes.Repeat([]byte{byte('a' + i)}, 1+i*37)
		want = append(want, chunk...)
		if i < paddedFrames {
			wire = append(wire, padClient(chunk, i*31%256)...)
		} else {
			wire = append(wire, chunk...)
		}
	}
	// One byte per read: frame boundaries must not depend on how the stream arrives.
	got, err := io.ReadAll(&unpadReader{r: iotestOneByte{bytes.NewReader(wire)}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("unpadded %q, want %q", got, want)
	}
}

func TestUnpadReaderTruncatedFrame(t *testing.T) {
	wire := padClient([]byte("hello"), 4)
	_, err := io.ReadAll(&unpadReader{r: bytes.NewReader(wire[:len(wire)-6])})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want unexpected EOF", err)
	}
}

type iotestOneByte struct{ r io.Reader }

func (o iotestOneByte) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func TestPadFrameRoundTrips(t *testing.T) {
	buf := make([]byte, 3+frameData+maxPadding)
	for _, n := range []int{1, 100, frameData} {
		copy(buf[3:], bytes.Repeat([]byte{7}, n))
		frame := padFrame(buf, n)
		if got := int(binary.BigEndian.Uint16(frame)); got != n {
			t.Fatalf("size %d, want %d", got, n)
		}
		if len(frame) != 3+n+int(frame[2]) || len(frame) > 65535 {
			t.Fatalf("frame of %d bytes for %d data, padding %d", len(frame), n, frame[2])
		}
	}
}

func TestPaddingNegotiation(t *testing.T) {
	for _, tc := range []struct {
		hdr    map[string]string
		padded bool
		reply  string
	}{
		{map[string]string{"Padding-Type-Request": "1, 0", "Padding": "~~~"}, true, "1"},
		{map[string]string{"Padding-Type-Request": "0"}, false, "0"},
		{map[string]string{"Padding-Type-Request": "7, 1"}, true, "1"},
		{map[string]string{"Padding": "~~~"}, true, ""}, // before negotiation existed
		{map[string]string{}, false, ""},
	} {
		h := http.Header{}
		for k, v := range tc.hdr {
			h.Set(k, v)
		}
		padded, reply := paddingType(h)
		if padded != tc.padded || reply != tc.reply {
			t.Errorf("%v: padded=%v reply=%q, want %v %q", tc.hdr, padded, reply, tc.padded, tc.reply)
		}
	}
	for range 100 {
		p := paddingHeader()
		if len(p) < 30 || len(p) > 61 || strings.Trim(p, nonindexCodes) != "" {
			t.Fatalf("padding header %q", p)
		}
	}
}

func TestVLESSRequest(t *testing.T) {
	id, ok := parseUUID(testUUID)
	if !ok {
		t.Fatal("uuid")
	}
	head := append([]byte{0}, id[:]...)
	head = append(head, 0, 1, 0x01, 0xbb)
	for host, tail := range map[string][]byte{
		"1.2.3.4":        {1, 1, 2, 3, 4},
		"::ffff:1.2.3.4": {1, 1, 2, 3, 4},
		"2001:db8::1":    append([]byte{3}, net.ParseIP("2001:db8::1").To16()...),
		"example.com":    append([]byte{2, 11}, "example.com"...),
	} {
		got := appendVLESSRequest(nil, id, vlessTCP, host, 443)
		if want := append(append([]byte(nil), head...), tail...); !bytes.Equal(got, want) {
			t.Errorf("%s: % x, want % x", host, got, want)
		}
	}
	if _, ok := parseUUID("not-a-uuid"); ok {
		t.Error("parsed a malformed uuid")
	}
}

// fakeXray stands in for naive-in: it reads the PROXY line and the VLESS request, checks
// the user and answers as an echo server for the requested target.
type fakeXray struct {
	ln net.Listener

	done chan struct{} // closed at cleanup: releases the stalled targets

	mu      sync.Mutex
	proxies []string
	targets []string
	conns   []net.Conn
}

func newFakeXray(t *testing.T) *fakeXray {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeXray{ln: ln, done: make(chan struct{})}
	t.Cleanup(func() { ln.Close(); close(f.done) })
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
	id, _ := parseUUID(testUUID)
	if hdr[0] != 0 || !bytes.Equal(hdr[1:17], id[:]) || (hdr[18] != vlessTCP && hdr[18] != vlessUDP) {
		return
	}
	var host string
	switch hdr[21] {
	case 2:
		n, _ := br.ReadByte()
		b := make([]byte, n)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = string(b)
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	}
	f.mu.Lock()
	f.proxies = append(f.proxies, line)
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(hdr[19:21]))))
	if hdr[18] == vlessUDP {
		target = "udp:" + target
	}
	f.targets = append(f.targets, target)
	f.conns = append(f.conns, c)
	f.mu.Unlock()
	if _, err := c.Write([]byte{0, 0}); err != nil {
		return
	}
	if host == "stall.test" { // a target that takes nothing: hold the connection, read none
		_, _ = c.Read(make([]byte, 0))
		<-f.done
		return
	}
	_, _ = io.Copy(c, br) // echo
}

// startLane serves Wrap(decoy) the way the panel does: h2c behind the PROXY listener.
func startLane(t *testing.T, s *Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	decoy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Decoy", "1")
		w.WriteHeader(http.StatusNotFound)
	})
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: s.Wrap(decoy), Protocols: p, HTTP2: HTTP2Config()}
	go func() { _ = srv.Serve(&proxyproto.Listener{Listener: ln}) }()
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

// h2cClient speaks prior-knowledge HTTP/2, as Xray's fallback hands the decoy a client.
func h2cClient() *http.Client {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: p}, Timeout: 10 * time.Second}
}

// connect opens a CONNECT stream; body is what the client sends.
func connect(t *testing.T, addr, auth, target string, body io.Reader, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodConnect, "http://"+addr, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = target
	req.URL.Host = addr
	req.URL.Path = ""
	req.URL.Scheme = "http"
	if auth != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth)))
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := h2cClient().Do(req)
	if err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	return resp
}

func TestUnauthenticatedConnectReachesTheDecoy(t *testing.T) {
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	addr := startLane(t, s)
	for name, auth := range map[string]string{
		"no credentials": "",
		"wrong password": testEmail + ":11111111-2222-3333-4444-555555555555",
		"wrong login":    "u8:" + testUUID,
		"password only":  ":" + testUUID,
	} {
		resp := connect(t, addr, auth, "example.com:443", http.NoBody, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || resp.Header.Get("X-Decoy") != "1" {
			t.Errorf("%s: %d, decoy=%q — want the decoy's answer", name, resp.StatusCode, resp.Header.Get("X-Decoy"))
		}
	}
	if len(fx.targets) != 0 {
		t.Fatalf("a refused CONNECT reached Xray: %v", fx.targets)
	}
}

func TestTunnelWithPadding(t *testing.T) {
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	addr := startLane(t, s)

	pr, pw := io.Pipe()
	resp := connect(t, addr, testEmail+":"+testUUID, "example.com:8443", pr,
		map[string]string{"Padding-Type-Request": "1, 0", "Padding": "~~~~~~~~~~~~~~~~"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.Header.Get("Padding-Type-Reply") != "1" || resp.Header.Get("Padding") == "" {
		t.Fatalf("padding not negotiated: %v", resp.Header)
	}

	// Ten messages: the first eight padded both ways, the rest plain.
	unpad := &unpadReader{r: resp.Body}
	for i := range 10 {
		msg := bytes.Repeat([]byte{byte('A' + i)}, 1000+i)
		frame := msg
		if i < paddedFrames {
			frame = padClient(msg, rand.IntN(256))
		}
		if _, err := pw.Write(frame); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(unpad, got); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if !bytes.Equal(got, msg) {
			t.Fatalf("message %d echoed wrong", i)
		}
	}
	pw.Close()

	fx.mu.Lock()
	defer fx.mu.Unlock()
	if len(fx.targets) != 1 || fx.targets[0] != "example.com:8443" {
		t.Fatalf("targets %v", fx.targets)
	}
	if !strings.HasPrefix(fx.proxies[0], "PROXY TCP4 127.0.0.1 127.0.0.1 ") {
		t.Fatalf("proxy line %q", fx.proxies[0])
	}
}

func TestTunnelWithoutPadding(t *testing.T) {
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	addr := startLane(t, s)

	pr, pw := io.Pipe()
	resp := connect(t, addr, testEmail+":"+testUUID, "1.2.3.4:80", pr, map[string]string{"Padding-Type-Request": "0"})
	defer resp.Body.Close()
	if resp.Header.Get("Padding-Type-Reply") != "0" {
		t.Fatalf("reply %q", resp.Header.Get("Padding-Type-Reply"))
	}
	go func() { _, _ = pw.Write([]byte("ping")) }()
	got := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo %q %v", got, err)
	}
	pw.Close()
	// The client's half-close ends the stream once Xray's side is done.
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("stream end: %v", err)
	}
}

// A user removed from naive-in loses the tunnels they already have.
func TestSetUsersEndsRemovedUsersTunnels(t *testing.T) {
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	addr := startLane(t, s)

	pr, pw := io.Pipe()
	defer pw.Close()
	resp := connect(t, addr, testEmail+":"+testUUID, "example.com:443", pr, map[string]string{"Padding-Type-Request": "0"}) //nolint:bodyclose // closed below
	defer resp.Body.Close()
	go func() { _, _ = pw.Write([]byte("x")) }()
	if _, err := io.ReadFull(resp.Body, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if s.Open() != 1 {
		t.Fatalf("%d open tunnels, want 1", s.Open())
	}

	s.SetUsers("", []User{{Email: "u8", UUID: "11111111-2222-3333-4444-555555555555"}})
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(resp.Body); done <- err }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the removed user's tunnel stayed open")
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.Open() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Open() != 0 {
		t.Fatalf("%d tunnels still tracked", s.Open())
	}

	// And cannot open a new one: the CONNECT goes to the decoy.
	resp2 := connect(t, addr, testEmail+":"+testUUID, "example.com:443", http.NoBody, nil)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("removed user got %d", resp2.StatusCode)
	}
}

// A client that stops reading leaves the tunnel blocked on HTTP/2 flow control, where
// closing Xray's side wakes nothing; removing the user must end it all the same, and
// reset it rather than let the client play out what is in flight.
func TestSetUsersEndsATunnelBlockedOnASlowClient(t *testing.T) {
	src := listen(t)
	go func() {
		c, err := src.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		if _, err := br.ReadString('\n'); err != nil { // PROXY line
			return
		}
		if _, err := io.ReadFull(br, make([]byte, 1+16+1+1+2+1+1+11)); err != nil { // VLESS, example.com
			return
		}
		_, _ = c.Write([]byte{0, 0})
		_, _ = io.Copy(c, zeroReader{}) // as fast as the tunnel takes it
	}()
	s := New(src.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	addr := startLane(t, s)

	pr, pw := io.Pipe()
	defer pw.Close()
	resp := connect(t, addr, testEmail+":"+testUUID, "example.com:443", pr, map[string]string{"Padding-Type-Request": "0"}) //nolint:bodyclose // closed below
	defer resp.Body.Close()
	if _, err := io.ReadFull(resp.Body, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // the windows fill; the handler blocks in Write

	s.SetUsers("", nil)
	deadline := time.Now().Add(3 * time.Second)
	for s.Open() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Open() != 0 {
		t.Fatal("the tunnel of a removed user stayed open while its client was not reading")
	}
	if _, err := io.Copy(io.Discard, resp.Body); err == nil {
		t.Fatal("the cut tunnel ended cleanly; want a reset")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// Over HTTP/1.1 a CONNECT is not a NaiveProxy client and goes to the decoy.
func TestHTTP1ConnectReachesTheDecoy(t *testing.T) {
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	addr := startLane(t, s)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	auth := base64.StdEncoding.EncodeToString([]byte(testEmail + ":" + testUUID))
	_, _ = io.WriteString(c, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Authorization: Basic "+auth+"\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("HTTP/1.1 CONNECT got %d", resp.StatusCode)
	}
}

func TestNoUsersLeavesEverythingToTheDecoy(t *testing.T) {
	s := New("127.0.0.1:1")
	addr := startLane(t, s)
	resp := connect(t, addr, testEmail+":"+testUUID, "example.com:443", http.NoBody, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d with no users", resp.StatusCode)
	}
}

func TestBasicAuth(t *testing.T) {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	for h, want := range map[string][2]string{
		"Basic " + enc("u1:pa:ss"): {"u1", "pa:ss"},
		"basic " + enc("u1:x"):     {"u1", "x"},
	} {
		l, p, ok := basicAuth(h)
		if !ok || l != want[0] || p != want[1] {
			t.Errorf("%q → %q %q %v", h, l, p, ok)
		}
	}
	for _, h := range []string{"", "Bearer x", "Basic !!!", "Basic " + enc("nocolon")} {
		if _, _, ok := basicAuth(h); ok {
			t.Errorf("%q accepted", h)
		}
	}
}

// One tunnel whose target stopped reading leaves the client's other tunnels on the same
// HTTP/2 connection moving: it may hold its own window, not the connection's.
func TestStalledTunnelLeavesTheOthersMoving(t *testing.T) {
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	addr := startLane(t, s)
	client := h2cClient() // one client, one connection: as NaiveProxy multiplexes
	open := func(target string, body io.Reader) *http.Response {
		req, _ := http.NewRequest(http.MethodConnect, "http://"+addr, body)
		req.Host = target
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(testEmail+":"+testUUID)))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("CONNECT %s: %v", target, err)
		}
		return resp
	}

	var sent atomic.Int64
	stall := open("stall.test:80", countingReader{io.LimitReader(zeroReader{}, 256<<20), &sent})
	defer stall.Body.Close()
	// Until it has filled whatever it can — kernel buffers, then windows — and stopped.
	for last := int64(-1); sent.Load() != last; time.Sleep(300 * time.Millisecond) {
		last = sent.Load()
	}

	pr, pw := io.Pipe()
	defer pw.Close()
	echo := open("example.com:80", pr)
	defer echo.Body.Close()
	body := echo.Body
	msg := bytes.Repeat([]byte("x"), 1<<20)
	done := make(chan error, 1)
	go func() {
		if _, err := pw.Write(msg); err != nil {
			done <- err
			return
		}
		got := make([]byte, len(msg))
		_, err := io.ReadFull(body, got)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second tunnel is stuck behind the stalled one")
	}
}

type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
