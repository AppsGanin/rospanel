package turnrelay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/turnrelay/freeturnref/rtpopus"
	"github.com/AppsGanin/rospanel/internal/turnrelay/freeturnref/rtpopus2"
	"github.com/AppsGanin/rospanel/internal/turnrelay/freeturnref/rtpopus3"
	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
)

// refCodec is a Free Turn Proxy client codec, as its own source defines it.
type refCodec interface {
	WrapInto(dst, payload []byte) (int, error)
	Unwrap(wire, dst []byte) (int, error)
	Overhead() int
}

// refMaskConn is the client side of the masked wire, built on Free Turn Proxy's codecs:
// every datagram out sealed by the codec, every datagram in opened by it.
type refMaskConn struct {
	net.PacketConn
	codec refCodec
	mu    sync.Mutex
}

func (c *refMaskConn) ReadFrom(p []byte) (int, net.Addr, error) {
	buf := make([]byte, 2048)
	for {
		n, addr, err := c.PacketConn.ReadFrom(buf)
		if err != nil {
			return 0, addr, err
		}
		m, err := c.codec.Unwrap(buf[:n], p)
		if err != nil {
			continue
		}
		return m, addr, nil
	}
}

func (c *refMaskConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	buf := make([]byte, len(p)+c.codec.Overhead())
	n, err := c.codec.WrapInto(buf, p)
	if err != nil {
		return 0, err
	}
	if _, err := c.PacketConn.WriteTo(buf[:n], addr); err != nil {
		return 0, err
	}
	return len(p), nil
}

func newMaskKey(t *testing.T) ([]byte, string) {
	t.Helper()
	key := make([]byte, MaskKeyLen)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key, hex.EncodeToString(key)
}

// dialFreeTurn connects the way Free Turn Proxy's client does (transport/dtlsdial and
// proxy/udprelay): DTLS with vk-turn-proxy's options over the masked wire, then the
// client id greeting in the given mode.
func dialFreeTurn(t *testing.T, port int, codec refCodec, mode byte) (*dtls.Conn, error) {
	t.Helper()
	cert, err := selfsign.GenerateSelfSigned()
	if err != nil {
		t.Fatal(err)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	raddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	conn, err := dtls.ClientWithOptions(&refMaskConn{PacketConn: pc, codec: codec}, raddr,
		dtls.WithCertificates(cert),
		dtls.WithInsecureSkipVerify(true),
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256),
		dtls.WithConnectionIDGenerator(dtls.OnlySendCIDGenerator()),
	)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	// clientsdb.WriteClientID: 1 byte length, the id, 1 byte mode.
	id := hex.EncodeToString(bytes.Repeat([]byte{0xAB}, 16))
	greeting := append(append([]byte{byte(len(id))}, id...), mode)
	if _, err := conn.Write(greeting); err != nil {
		return nil, err
	}
	return conn, nil
}

// recordingEcho is echoUDP that also keeps what it was sent, to show the greeting never
// reaches the tunnel.
func recordingEcho(t *testing.T) (string, func() [][]byte) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	var mu sync.Mutex
	var got [][]byte
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			mu.Lock()
			got = append(got, append([]byte(nil), buf[:n]...))
			mu.Unlock()
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().String(), func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][]byte(nil), got...)
	}
}

// Free Turn Proxy's clients, on each of its three wire profiles and with its own codecs,
// get through the relay both ways; their greeting is dropped before the tunnel; and an
// unmasked vk-turn-proxy client still works on the same port under the same key.
func TestFreeTurnClientsThroughTheRelay(t *testing.T) {
	key, keyHex := newMaskKey(t)
	target, received := recordingEcho(t)
	r := New()
	t.Cleanup(r.Close)
	port := freeUDPPort(t)
	if err := r.Sync([]Spec{{Port: port, Target: target, MaskKey: keyHex}}); err != nil {
		t.Fatal(err)
	}

	codecs := map[string]func() (refCodec, error){
		"rtpopus":  func() (refCodec, error) { return rtpopus.NewConn(key, false) },
		"rtpopus2": func() (refCodec, error) { return rtpopus2.NewConn(key, false) },
		"rtpopus3": func() (refCodec, error) { return rtpopus3.NewConn(key, false) },
	}
	for name, mk := range codecs {
		t.Run(name, func(t *testing.T) {
			codec, err := mk()
			if err != nil {
				t.Fatal(err)
			}
			conn, err := dialFreeTurn(t, port, codec, 1)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			roundTrip(t, conn, bytes.Repeat([]byte{4, 0, 0, 0}, 37))          // a WireGuard-sized datagram
			roundTrip(t, conn, bytes.Repeat([]byte{name[len(name)-1]}, 1312)) // a full one at MTU 1280
		})
	}
	// Plain DTLS on the same port.
	roundTrip(t, dial(t, port), []byte("vk-turn-proxy, unmasked"))

	for _, pkt := range received() {
		if ok, _ := isClientIDRecord(pkt); ok {
			t.Errorf("a greeting reached the tunnel: %q", pkt)
		}
	}
}

// A client with another key never completes a handshake: its datagrams do not open, so
// the relay does not even admit its address.
func TestFreeTurnWrongKeyIsRefused(t *testing.T) {
	_, keyHex := newMaskKey(t)
	other, _ := newMaskKey(t)
	r := New()
	t.Cleanup(r.Close)
	port := freeUDPPort(t)
	if err := r.Sync([]Spec{{Port: port, Target: echoUDP(t), MaskKey: keyHex}}); err != nil {
		t.Fatal(err)
	}
	codec, err := rtpopus3.NewConn(other, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dialFreeTurn(t, port, codec, 1); err == nil {
		t.Fatal("a client with the wrong key completed the handshake")
	}

	// Without a key at all, masked clients are not served.
	r2 := New()
	t.Cleanup(r2.Close)
	port2 := freeUDPPort(t)
	if err := r2.Sync([]Spec{{Port: port2, Target: echoUDP(t)}}); err != nil {
		t.Fatal(err)
	}
	codec2, _ := rtpopus3.NewConn(other, false)
	if _, err := dialFreeTurn(t, port2, codec2, 1); err == nil {
		t.Fatal("a masked client was served by a relay with no key")
	}
}

// A client asking for the stream mode (Free Turn Proxy's -mode tcp) is one this relay
// cannot serve: its leg is closed rather than fed to WireGuard as garbage.
func TestFreeTurnStreamModeIsRefused(t *testing.T) {
	key, keyHex := newMaskKey(t)
	target, received := recordingEcho(t)
	r := New()
	t.Cleanup(r.Close)
	port := freeUDPPort(t)
	if err := r.Sync([]Spec{{Port: port, Target: target, MaskKey: keyHex}}); err != nil {
		t.Fatal(err)
	}
	codec, _ := rtpopus3.NewConn(key, false)
	conn, err := dialFreeTurn(t, port, codec, 2)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("stream bytes"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 64)); err == nil {
		t.Error("the stream-mode leg answered")
	}
	if n := len(received()); n != 0 {
		t.Errorf("the stream-mode client's bytes reached the tunnel (%d datagrams)", n)
	}
}

// The profile is read off the datagram: the first byte and the extension length.
func TestDetectProfile(t *testing.T) {
	ext := func(words uint16) []byte {
		b := make([]byte, 40)
		b[0], b[12], b[13] = 0x90, 0xBE, 0xDE
		b[14], b[15] = byte(words>>8), byte(words)
		return b
	}
	for name, c := range map[string]struct {
		b    []byte
		want maskProfile
	}{
		"rtpopus":        {append([]byte{0x80}, make([]byte, 39)...), profileRTPOpus},
		"rtpopus2":       {ext(2), profileRTPOpus2},
		"rtpopus3":       {ext(3), profileRTPOpus3},
		"other ext":      {ext(5), profileUnknown},
		"dtls handshake": {append([]byte{22}, make([]byte, 20)...), profilePlain},
		"dtls cid":       {append([]byte{25}, make([]byte, 20)...), profilePlain},
		"noise":          {[]byte{0x42, 1, 2}, profileUnknown},
		"empty":          {nil, profileUnknown},
	} {
		if got := detectProfile(c.b); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
