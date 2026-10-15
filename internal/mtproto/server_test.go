package mtproto

import (
	"bufio"
	"bytes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testDomain = "vpn.example.com"

// helloRecord builds a ClientHello the way tdlib does for an "ee" secret: SNI domain, a
// random that is the HMAC of the record XOR the time.
func helloRecord(t *testing.T, secret []byte, domain string, at time.Time) []byte {
	t.Helper()
	var body bytes.Buffer
	body.Write([]byte{3, 3})
	body.Write(make([]byte, 32)) // random, filled below
	sid := make([]byte, 32)
	_, _ = rand.Read(sid)
	body.WriteByte(32)
	body.Write(sid)
	suites := []byte{0x0a, 0x0a, 0x13, 0x01, 0x13, 0x02, 0x13, 0x03}
	_ = binary.Write(&body, binary.BigEndian, uint16(len(suites)))
	body.Write(suites)
	body.Write([]byte{1, 0}) // compression: null
	var sni bytes.Buffer
	_ = binary.Write(&sni, binary.BigEndian, uint16(3+len(domain)))
	sni.WriteByte(0)
	_ = binary.Write(&sni, binary.BigEndian, uint16(len(domain)))
	sni.WriteString(domain)
	var ext bytes.Buffer
	ext.Write([]byte{0, 0})
	_ = binary.Write(&ext, binary.BigEndian, uint16(sni.Len()))
	ext.Write(sni.Bytes())
	ext.Write([]byte{0x00, 0x15, 0x00, 0x10}) // padding
	ext.Write(make([]byte, 16))
	_ = binary.Write(&body, binary.BigEndian, uint16(ext.Len()))
	body.Write(ext.Bytes())

	msg := append([]byte{helloClient, 0, byte(body.Len() >> 8), byte(body.Len())}, body.Bytes()...)
	rec := append([]byte{recordHandshake, 3, 1, byte(len(msg) >> 8), byte(len(msg))}, msg...)
	mac := hmac.New(sha256.New, secret)
	mac.Write(rec)
	sum := mac.Sum(nil)
	var ts [4]byte
	binary.LittleEndian.PutUint32(ts[:], uint32(at.Unix()))
	for i := range 4 {
		sum[28+i] ^= ts[i]
	}
	copy(rec[randomOffset:], sum)
	return rec
}

// client is a Telegram client's side of a fake TLS connection.
type client struct {
	conn       net.Conn
	rc         *recordConn
	send, recv cipher.Stream
}

func dialClient(t *testing.T, addr string, secret []byte, dc int) (*client, error) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	hello := helloRecord(t, secret, testDomain, time.Now())
	if _, err := c.Write(hello); err != nil {
		return nil, err
	}
	// The server's flight: ServerHello, CCS, one application-data record. Its random is
	// the HMAC of our random and the flight with the random zeroed.
	br := bufio.NewReader(c)
	var flight []byte
	for range 3 {
		var hdr [5]byte
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return nil, err
		}
		body := make([]byte, binary.BigEndian.Uint16(hdr[3:]))
		if _, err := io.ReadFull(br, body); err != nil {
			return nil, err
		}
		flight = append(append(flight, hdr[:]...), body...)
	}
	got := bytes.Clone(flight[randomOffset : randomOffset+32])
	clear(flight[randomOffset : randomOffset+32])
	mac := hmac.New(sha256.New, secret)
	mac.Write(hello[randomOffset : randomOffset+32])
	mac.Write(flight)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return nil, errors.New("server random does not prove the secret")
	}
	rc := newRecordConn(br, c)
	_, _ = c.Write([]byte{recordCCS, 3, 3, 0, 1, 1})
	opening, send, recv := openFrame(dc, secret)
	if _, err := rc.Write(opening); err != nil {
		return nil, err
	}
	return &client{conn: c, rc: rc, send: send, recv: recv}, nil
}

func (c *client) roundTrip(t *testing.T, msg []byte) []byte {
	t.Helper()
	out := bytes.Clone(msg)
	c.send.XORKeyStream(out, out)
	if _, err := c.rc.Write(out); err != nil {
		t.Fatalf("write: %v", err)
	}
	back := make([]byte, len(msg))
	if _, err := io.ReadFull(c.rc, back); err != nil {
		t.Fatalf("read: %v", err)
	}
	c.recv.XORKeyStream(back, back)
	return back
}

// fakeXray stands for Xray's mtproto-in and for Telegram behind it: it reads the PROXY
// line and the VLESS request, answers, then opens the obfuscated stream as a data
// centre would and echoes what comes in upper-cased.
type fakeXray struct {
	ln      net.Listener
	mu      sync.Mutex
	targets []string
	users   []string
	srcs    []string
}

func newFakeXray(t *testing.T) *fakeXray {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	x := &fakeXray{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go x.serve(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return x
}

func (x *fakeXray) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	var hdr [18]byte // version, uuid, addons length
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return
	}
	if _, err := br.Discard(int(hdr[17])); err != nil {
		return
	}
	var req [4]byte // command, port, address type
	if _, err := io.ReadFull(br, req[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(req[1:3])
	var host string
	switch req[3] {
	case 1:
		var a [4]byte
		_, _ = io.ReadFull(br, a[:])
		host = net.IP(a[:]).String()
	default:
		return
	}
	x.mu.Lock()
	x.targets = append(x.targets, net.JoinHostPort(host, strconv.Itoa(int(port))))
	x.users = append(x.users, hex.EncodeToString(hdr[1:17]))
	x.srcs = append(x.srcs, strings.Fields(line)[1])
	x.mu.Unlock()
	if _, err := c.Write([]byte{0, 0}); err != nil {
		return
	}
	_, recv, send, err := readClientFrame(br, nil)
	if err != nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			recv.XORKeyStream(buf[:n], buf[:n])
			out := bytes.ToUpper(buf[:n])
			send.XORKeyStream(out, out)
			if _, err := c.Write(out); err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// fakeFront is the site a connection that is not the lane's is handed to: it records
// the first bytes it gets.
type fakeFront struct {
	ln  net.Listener
	got chan []byte
}

func newFakeFront(t *testing.T) *fakeFront {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFront{ln: ln, got: make(chan []byte, 8)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				buf := make([]byte, 64*1024)
				n, _ := io.ReadAtLeast(c, buf, 5)
				f.got <- bytes.Clone(buf[:n])
				_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

const (
	uuidA = "11111111-2222-3333-4444-555555555555"
	uuidB = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func startLane(t *testing.T) (*Server, string, *fakeXray, *fakeFront) {
	t.Helper()
	x, f := newFakeXray(t), newFakeFront(t)
	s := New(x.ln.Addr().String())
	port := freePort(t)
	err := s.Apply(Listen{Port: port, Domain: testDomain, Front: f.ln.Addr().String()}, "",
		[]User{{Email: "u1", UUID: uuidA}, {Email: "u2", UUID: uuidB}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, "127.0.0.1:" + strconv.Itoa(port), x, f
}

func TestTunnelReachesTheDataCentre(t *testing.T) {
	t.Parallel()
	s, addr, x, _ := startLane(t)
	secret := Secret(uuidB)
	c, err := dialClient(t, addr, secret[:], -4)
	if err != nil {
		t.Fatal(err)
	}
	defer c.conn.Close()
	for _, msg := range []string{"hello telegram", strings.Repeat("x", 40000)} {
		if got := c.roundTrip(t, []byte(msg)); string(got) != strings.ToUpper(msg) {
			t.Fatalf("echo %q…", got[:min(len(got), 20)])
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.targets) != 1 || x.targets[0] != "149.154.167.91:443" {
		t.Fatalf("media DC 4 dialed as %v", x.targets)
	}
	if x.users[0] != strings.ReplaceAll(uuidB, "-", "") {
		t.Fatalf("entered Xray as %s", x.users[0])
	}
	if x.srcs[0] != "TCP4" || s.Open() != 1 {
		t.Fatalf("PROXY line %v, open %d", x.srcs, s.Open())
	}
}

func TestStrangersGoToTheFront(t *testing.T) {
	t.Parallel()
	_, addr, x, f := startLane(t)
	cases := map[string]func() []byte{
		"no secret of ours": func() []byte {
			wrong := Secret("99999999-9999-9999-9999-999999999999")
			return helloRecord(t, wrong[:], testDomain, time.Now())
		},
		"another SNI": func() []byte {
			s := Secret(uuidA)
			return helloRecord(t, s[:], "other.example.org", time.Now())
		},
		"stale hello": func() []byte {
			s := Secret(uuidA)
			return helloRecord(t, s[:], testDomain, time.Now().Add(-10*time.Minute))
		},
		"plain HTTP": func() []byte { return []byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n") },
	}
	for name, mk := range cases {
		sent := mk()
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = c.Write(sent)
		select {
		case got := <-f.got:
			if !bytes.HasPrefix(sent, got) && !bytes.HasPrefix(got, sent[:5]) {
				t.Errorf("%s: the front got %q", name, got[:min(len(got), 16)])
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: never reached the front", name)
		}
		reply, _ := io.ReadAll(c)
		if !bytes.HasPrefix(reply, []byte("HTTP/1.1 400")) {
			t.Errorf("%s: the client got %q, not the front's answer", name, reply)
		}
		_ = c.Close()
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.targets) != 0 {
		t.Fatalf("a stranger reached Telegram: %v", x.targets)
	}
}

func TestReplayedHelloGoesToTheFront(t *testing.T) {
	t.Parallel()
	_, addr, _, f := startLane(t)
	secret := Secret(uuidA)
	hello := helloRecord(t, secret[:], testDomain, time.Now())
	first, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	_, _ = first.Write(hello)
	var hdr [5]byte
	if _, err := io.ReadFull(first, hdr[:]); err != nil || hdr[0] != recordHandshake {
		t.Fatalf("the genuine hello was not answered: %v %x", err, hdr)
	}
	again, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	_, _ = again.Write(hello)
	select {
	case <-f.got:
	case <-time.After(5 * time.Second):
		t.Fatal("a replayed hello was answered as a client")
	}
}

func TestApplyEndsARemovedUsersConnections(t *testing.T) {
	t.Parallel()
	s, addr, x, f := startLane(t)
	secret := Secret(uuidA)
	c, err := dialClient(t, addr, secret[:], 2)
	if err != nil {
		t.Fatal(err)
	}
	defer c.conn.Close()
	if got := c.roundTrip(t, []byte("ping")); string(got) != "PING" {
		t.Fatalf("echo %q", got)
	}
	l := Listen{Port: s.Listening().Port, Domain: testDomain, Front: f.ln.Addr().String()}
	if err := s.Apply(l, x.ln.Addr().String(), []User{{Email: "u2", UUID: uuidB}}); err != nil {
		t.Fatal(err)
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.Read(make([]byte, 1)); err == nil || errors.Is(err, errDeadline(err)) {
		t.Fatalf("the removed user's connection is still open: %v", err)
	}
	if _, err := dialClient(t, addr, secret[:], 2); err == nil {
		t.Fatal("a removed user was let in again")
	}
}

// errDeadline reports a read that timed out rather than one the server ended.
func errDeadline(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return err
	}
	return errors.New("not a timeout")
}

func TestHelloSplitOverRecords(t *testing.T) {
	t.Parallel()
	secret := Secret(uuidA)
	rec := helloRecord(t, secret[:], testDomain, time.Now())
	msg := rec[5:]
	var split []byte
	for _, part := range [][]byte{msg[:3], msg[3:100], msg[100:]} {
		split = append(split, recordHandshake, 3, 1, byte(len(part)>>8), byte(len(part)))
		split = append(split, part...)
	}
	h, err := readClientHello(bytes.NewReader(split))
	if err != nil {
		t.Fatal(err)
	}
	if !h.verify(secret[:], time.Now(), skew) || !h.hasSNI(testDomain) {
		t.Fatal("a hello split over records does not verify")
	}
}

func TestSecretAndLink(t *testing.T) {
	t.Parallel()
	hexs := SecretHex(uuidA, testDomain)
	if !strings.HasPrefix(hexs, "ee") || len(hexs) != 2+32+2*len(testDomain) {
		t.Fatalf("secret %s", hexs)
	}
	if SecretHex(uuidB, testDomain) == hexs {
		t.Fatal("two users share a secret")
	}
	link := Link("vpn.example.com", 8443, testDomain, uuidA)
	if !strings.HasPrefix(link, "tg://proxy?") || !strings.Contains(link, "port=8443") || !strings.Contains(link, "secret="+hexs) {
		t.Fatalf("link %s", link)
	}
	if !strings.HasPrefix(WebLink("h", 1, testDomain, uuidA), "https://t.me/proxy?") {
		t.Fatal("web link")
	}
}

// A tunnel gives its handshake slot back: the slots bound handshakes, not users online.
func TestTunnelFreesItsHandshakeSlot(t *testing.T) {
	t.Parallel()
	s, addr, _, _ := startLane(t)
	secret := Secret(uuidA)
	var clients []*client
	for range 3 {
		c, err := dialClient(t, addr, secret[:], 2)
		if err != nil {
			t.Fatal(err)
		}
		defer c.conn.Close()
		if got := c.roundTrip(t, []byte("x")); string(got) != "X" {
			t.Fatalf("echo %q", got)
		}
		clients = append(clients, c)
	}
	if n := len(s.slots); n != 0 {
		t.Fatalf("%d handshake slots held by open tunnels", n)
	}
	if s.Open() != len(clients) {
		t.Fatalf("open %d", s.Open())
	}
}

// A client that sends its request and closes its sending half still gets the front
// site's answer, as it would from the site itself.
func TestFrontAnswersAfterHalfClose(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.ReadAll(c) // until the client's half-close arrives
		time.Sleep(100 * time.Millisecond)
		_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
	}()
	x := newFakeXray(t)
	s := New(x.ln.Addr().String())
	port := freePort(t)
	if err := s.Apply(Listen{Port: port, Domain: testDomain, Front: ln.Addr().String()}, "", nil); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	_ = c.(*net.TCPConn).CloseWrite()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	reply, _ := io.ReadAll(c)
	if !bytes.HasPrefix(reply, []byte("HTTP/1.1 400")) {
		t.Fatalf("after a half-close the client got %q", reply)
	}
}

// A hello is remembered for as long as it can pass the time check after it was first
// seen: twice the skew, across a rotation.
func TestReplayCacheCoversTheTimeWindow(t *testing.T) {
	t.Parallel()
	var r replayCache
	var a, b [randomLen]byte
	a[0], b[0] = 1, 2
	if r.seen(a) {
		t.Fatal("a fresh hello read as a replay")
	}
	// Just before the cache would rotate, a second hello comes in, then the rotation.
	r.rotated = time.Now().Add(-2*skew + time.Second)
	if r.seen(b) {
		t.Fatal("a fresh hello read as a replay")
	}
	r.rotated = time.Now().Add(-2*skew - time.Second)
	if !r.seen(b) {
		t.Fatal("a hello seen less than a skew ago was forgotten at the rotation")
	}
}

func TestOversizedHelloGoesToTheFront(t *testing.T) {
	t.Parallel()
	_, addr, x, f := startLane(t)
	secret := Secret(uuidA)
	rec := helloRecord(t, secret[:], testDomain, time.Now())
	// The same hello padded past the scan limit: correctly made, still not checked.
	msg := rec[5:]
	bodyLen := int(msg[2])<<8 | int(msg[3])
	pad := make([]byte, maxHelloScan)
	big := append([]byte{}, msg[:4+bodyLen]...)
	big = append(big, pad...)
	n := len(big) - 4
	big[1], big[2], big[3] = byte(n>>16), byte(n>>8), byte(n)
	var wire []byte
	for i := 0; i < len(big); i += 4000 {
		part := big[i:min(i+4000, len(big))]
		wire = append(wire, recordHandshake, 3, 1, byte(len(part)>>8), byte(len(part)))
		wire = append(wire, part...)
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write(wire)
	select {
	case <-f.got:
	case <-time.After(5 * time.Second):
		t.Fatal("an oversized hello was not handed to the front")
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.targets) != 0 {
		t.Fatal("an oversized hello reached Telegram")
	}
}

// A hello split over several records — as some clients send it — gets through end to end.
func TestSplitHelloEndToEnd(t *testing.T) {
	t.Parallel()
	_, addr, _, _ := startLane(t)
	secret := Secret(uuidA)
	rec := helloRecord(t, secret[:], testDomain, time.Now())
	msg := rec[5:]
	var split []byte
	for _, part := range [][]byte{msg[:3], msg[3:60], msg[60:]} {
		split = append(split, recordHandshake, 3, 1, byte(len(part)>>8), byte(len(part)))
		split = append(split, part...)
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write(split)
	var hdr [5]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil || hdr[0] != recordHandshake {
		t.Fatalf("a split hello was not answered: %v %x", err, hdr)
	}
}
