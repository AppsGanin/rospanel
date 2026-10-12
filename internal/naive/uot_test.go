package naive

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"
)

// uotStream opens a UDP-over-TCP stream and returns its writer and reader.
func uotStream(t *testing.T, addr, magic string) (io.WriteCloser, io.Reader) {
	t.Helper()
	pr, pw := io.Pipe()
	resp := connect(t, addr, testEmail+":"+testUUID, magic+":0", pr, map[string]string{"Padding-Type-Request": "1, 0"}) //nolint:bodyclose // closed by the cleanup
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Padding-Type-Reply") != "1" {
		t.Fatalf("CONNECT %s: %d %v", magic, resp.StatusCode, resp.Header)
	}
	return &padWriter{w: pw}, &unpadReader{r: resp.Body}
}

// padWriter pads the first frames as the client does.
type padWriter struct {
	w      io.WriteCloser
	frames int
}

func (p *padWriter) Write(b []byte) (int, error) {
	out := b
	if p.frames < paddedFrames {
		p.frames++
		out = padClient(b, p.frames*17%256)
	}
	if _, err := p.w.Write(out); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (p *padWriter) Close() error { return p.w.Close() }

func packet(payload string) []byte {
	return append(binary.BigEndian.AppendUint16(nil, uint16(len(payload))), payload...)
}

// pktAddr is an address in the per-packet encoding: 00 IPv4, 01 IPv6, 02 domain.
func pktAddr(host string, port uint16) []byte {
	var b []byte
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		b = append([]byte{0}, ip.To4()...)
	} else if ip != nil {
		b = append([]byte{1}, ip.To16()...)
	} else {
		b = append([]byte{2, byte(len(host))}, host...)
	}
	return binary.BigEndian.AppendUint16(b, port)
}

func readFull(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(r, b); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read %d bytes: %v", n, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no %d bytes within 5s", n)
	}
	return b
}

func uotLane(t *testing.T) (*fakeXray, *Server, string) {
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	return fx, s, startLane(t, s)
}

// A connected stream is one VLESS UDP connection to its destination, packets as they are.
func TestUDPOverTCPConnected(t *testing.T) {
	fx, _, addr := uotLane(t)
	w, r := uotStream(t, addr, uotMagic)
	req := append([]byte{1, 0x01, 8, 8, 4, 4}, 0, 53) // connected, SOCKS IPv4 8.8.4.4:53
	if _, err := w.Write(append(req, packet("query-1")...)); err != nil {
		t.Fatal(err)
	}
	if got := readFull(t, r, 2+7); !bytes.Equal(got, packet("query-1")) {
		t.Fatalf("reply %q", got)
	}
	for i := range 10 { // past the padded frames
		p := packet(string(rune('a'+i)) + "-more")
		if _, err := w.Write(p); err != nil {
			t.Fatal(err)
		}
		if got := readFull(t, r, len(p)); !bytes.Equal(got, p) {
			t.Fatalf("reply %d %q", i, got)
		}
	}
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if !slices.Equal(fx.targets, []string{"udp:8.8.4.4:53"}) {
		t.Fatalf("targets %v", fx.targets)
	}
}

// In packet mode every destination gets its own VLESS UDP connection, and every reply
// comes back addressed with the destination it is from.
func TestUDPOverTCPPackets(t *testing.T) {
	for _, magic := range []string{uotMagic, uotMagicLegacy} {
		t.Run(magic, func(t *testing.T) {
			fx, _, addr := uotLane(t)
			w, r := uotStream(t, addr, magic)
			if magic == uotMagic {
				// Not connected; the request's address is unused in this mode.
				if _, err := w.Write([]byte{0, 0x03, 4, 'n', 'o', 'n', 'e', 0, 0}); err != nil {
					t.Fatal(err)
				}
			}
			dests := []struct {
				host string
				port uint16
			}{{"1.1.1.1", 53}, {"2001:db8::1", 443}, {"stun.example.com", 3478}, {"1.1.1.1", 53}}
			for i, d := range dests {
				payload := string(rune('A'+i)) + "-payload"
				msg := append(pktAddr(d.host, d.port), packet(payload)...)
				if _, err := w.Write(msg); err != nil {
					t.Fatal(err)
				}
				if got := readFull(t, r, len(msg)); !bytes.Equal(got, msg) {
					t.Fatalf("reply from %s: % x, want % x", d.host, got, msg)
				}
			}
			fx.mu.Lock()
			defer fx.mu.Unlock()
			want := []string{"udp:1.1.1.1:53", "udp:[2001:db8::1]:443", "udp:stun.example.com:3478"}
			if !slices.Equal(fx.targets, want) {
				t.Fatalf("targets %v, want %v (one connection per destination)", fx.targets, want)
			}
		})
	}
}

// Past maxUDPDests destinations the one idle longest makes room.
func TestUDPOverTCPEvictsTheIdlest(t *testing.T) {
	fx, s, addr := uotLane(t)
	w, r := uotStream(t, addr, uotMagicLegacy)
	send := func(port uint16) {
		t.Helper()
		msg := append(pktAddr("10.0.0.1", port), packet("x")...)
		if _, err := w.Write(msg); err != nil {
			t.Fatal(err)
		}
		readFull(t, r, len(msg))
	}
	for p := range maxUDPDests {
		send(uint16(1000 + p))
	}
	send(1000) // the first is the most recent now; the second is the idlest
	send(5000)
	fx.mu.Lock()
	n := len(fx.targets)
	fx.mu.Unlock()
	if n != maxUDPDests+1 {
		t.Fatalf("%d connections opened, want %d", n, maxUDPDests+1)
	}
	var st *stream
	s.mu.Lock()
	for x := range s.streams {
		st = x
	}
	s.mu.Unlock()
	st.mu.Lock()
	open := len(st.conns)
	st.mu.Unlock()
	if open != maxUDPDests {
		t.Fatalf("%d connections held, want %d", open, maxUDPDests)
	}
	send(1000) // still open: no new connection
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if len(fx.targets) != maxUDPDests+1 {
		t.Fatalf("the recently used destination was reopened: %d connections", len(fx.targets))
	}
}

// A removed user's UDP stream ends like a TCP one.
func TestUDPOverTCPCut(t *testing.T) {
	_, s, addr := uotLane(t)
	w, r := uotStream(t, addr, uotMagicLegacy)
	msg := append(pktAddr("9.9.9.9", 53), packet("q")...)
	if _, err := w.Write(msg); err != nil {
		t.Fatal(err)
	}
	readFull(t, r, len(msg))
	s.SetUsers("", nil)
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, r); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the cut stream ended cleanly; want a reset")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the removed user's UDP stream stayed open")
	}
}

// The magic hosts ask for UDP over TCP whatever the port; an unknown address family
// ends the stream rather than being guessed at.
func TestUoTAddressing(t *testing.T) {
	for host, want := range map[string]uotVersion{uotMagic: uotV2, uotMagicLegacy: uotV1, "example.com": uotNone} {
		_, _, v, err := splitTarget(host + ":443")
		if err != nil || v != want {
			t.Errorf("%s: %v %v, want %v", host, v, err, want)
		}
	}
	if _, _, v, err := splitTarget(uotMagic + ":0"); err != nil || v != uotV2 {
		t.Errorf("port 0: %v %v", v, err)
	}
	if _, _, _, err := readAddr(bufio.NewReader(bytes.NewReader([]byte{9, 1, 2})), packetFamilies); !errors.Is(err, errBadFamily) {
		t.Errorf("unknown family: %v", err)
	}
}
