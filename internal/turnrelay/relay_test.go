package turnrelay

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
)

// echoUDP stands in for the WireGuard inbound: it sends every datagram back.
func echoUDP(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().String()
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

// dial connects the way vk-turn-proxy's client does (client/main.go, dtlsFunc): the
// same suite, extended master secret and connection-id options. A relay that drifts
// from them fails here the way it would fail against the real client.
func dial(t *testing.T, port int) *dtls.Conn {
	t.Helper()
	cert, err := selfsign.GenerateSelfSigned()
	if err != nil {
		t.Fatal(err)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	raddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	conn, err := dtls.ClientWithOptions(pc, raddr,
		dtls.WithCertificates(cert),
		dtls.WithInsecureSkipVerify(true),
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256),
		dtls.WithConnectionIDGenerator(dtls.OnlySendCIDGenerator()),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func roundTrip(t *testing.T, conn *dtls.Conn, payload []byte) {
	t.Helper()
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Fatalf("got %q, want %q", buf[:n], payload)
	}
}

func TestRelayCarriesPacketsBothWays(t *testing.T) {
	r := New()
	t.Cleanup(r.Close)
	port := freeUDPPort(t)
	if err := r.Sync([]Spec{{Port: port, Target: echoUDP(t)}}); err != nil {
		t.Fatal(err)
	}
	conn := dial(t, port)
	// A WireGuard handshake initiation is 148 bytes; a full packet at MTU 1280 is the
	// largest a client sends. Both must arrive whole.
	roundTrip(t, conn, bytes.Repeat([]byte{1}, 148))
	roundTrip(t, conn, bytes.Repeat([]byte{2}, 1280+32))

	// Legs are independent: a second client's packets come back to it, not the first.
	other := dial(t, port)
	roundTrip(t, other, []byte("second leg"))
	roundTrip(t, conn, []byte("first leg still up"))
}

func TestSyncKeepsRunningRelaysAndStopsDroppedOnes(t *testing.T) {
	r := New()
	t.Cleanup(r.Close)
	target := echoUDP(t)
	a, b := freeUDPPort(t), freeUDPPort(t)
	if err := r.Sync([]Spec{{Port: a, Target: target}, {Port: b, Target: target}}); err != nil {
		t.Fatal(err)
	}
	conn := dial(t, a)
	roundTrip(t, conn, []byte("before"))

	// Dropping b must not disturb a's open tunnel.
	if err := r.Sync([]Spec{{Port: a, Target: target}}); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, conn, []byte("after"))
	if got := r.Ports(); len(got) != 1 || got[0] != a {
		t.Fatalf("ports = %v, want [%d]", got, a)
	}
	// b's port is free again.
	pc, err := net.ListenPacket("udp", net.JoinHostPort("", strconv.Itoa(b)))
	if err != nil {
		t.Fatalf("port %d still held after its relay was dropped: %v", b, err)
	}
	pc.Close()
}

func TestSyncReportsATakenPortAndStartsTheRest(t *testing.T) {
	busy, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	taken := busy.LocalAddr().(*net.UDPAddr).Port

	r := New()
	t.Cleanup(r.Close)
	target := echoUDP(t)
	free := freeUDPPort(t)
	err = r.Sync([]Spec{{Port: taken, Target: target}, {Port: free, Target: target}})
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(taken)) {
		t.Fatalf("err = %v, want one naming udp/%d", err, taken)
	}
	roundTrip(t, dial(t, free), []byte("still serving"))
}

func TestNilRelayIsANoOp(t *testing.T) {
	var r *Relay
	if err := r.Sync([]Spec{{Port: 1, Target: "127.0.0.1:1"}}); err != nil {
		t.Fatal(err)
	}
	if r.Ports() != nil {
		t.Fatal("a nil relay reports ports")
	}
	r.Close()
}
