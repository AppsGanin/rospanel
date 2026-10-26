package turnrelay

import (
	"bytes"
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"syscall"
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

// syncOnFreePorts starts r on specs, each on a free port, and returns the ports. A port
// found free can be taken by another socket before the relay binds it (on any address:
// the relay listens on all of them), so a bind refused as in use is tried again on
// other ports.
func syncOnFreePorts(t *testing.T, r *Relay, specs ...Spec) []int {
	t.Helper()
	for range 20 {
		var ports []int
		for len(ports) < len(specs) {
			if p := freeUDPPort(t); !slices.Contains(ports, p) {
				ports = append(ports, p)
			}
		}
		list := slices.Clone(specs)
		for i := range list {
			list[i].Port = ports[i]
		}
		err := r.Sync(list)
		if err == nil {
			return ports
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatal(err)
		}
	}
	t.Fatal("no free ports for the relay after 20 tries")
	return nil
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

// roundTrip sends payload and waits for it to come back. UDP may drop a datagram on a
// loaded machine, as WireGuard expects, so it is sent again every second until it
// returns or 5 s pass; an echo of an earlier copy is skipped.
func roundTrip(t *testing.T, conn *dtls.Conn, payload []byte) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 2048)
	var got []byte
	for time.Now().Before(deadline) {
		if _, err := conn.Write(payload); err != nil {
			t.Fatal(err)
		}
		wait := time.Now().Add(time.Second)
		if wait.After(deadline) {
			wait = deadline
		}
		_ = conn.SetReadDeadline(wait)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break // nothing within the second: send again
			}
			if bytes.Equal(buf[:n], payload) {
				return
			}
			got = append(got[:0], buf[:n]...)
		}
	}
	if got != nil {
		t.Fatalf("got %q, want %q", got, payload)
	}
	t.Fatalf("read back: nothing within 5 s")
}

func TestRelayCarriesPacketsBothWays(t *testing.T) {
	r := New()
	t.Cleanup(r.Close)
	port := syncOnFreePorts(t, r, Spec{Target: echoUDP(t)})[0]
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
	ports := syncOnFreePorts(t, r, Spec{Target: target}, Spec{Target: target})
	a, b := ports[0], ports[1]
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
	// The free port can be taken before the relay binds it (see syncOnFreePorts).
	var free int
	for range 20 {
		free = freeUDPPort(t)
		err = r.Sync([]Spec{{Port: taken, Target: target}, {Port: free, Target: target}})
		if !slices.Contains(r.Failed(), free) {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(taken)) {
		t.Fatalf("err = %v, want one naming udp/%d", err, taken)
	}
	roundTrip(t, dial(t, free), []byte("still serving"))
	if f := r.Failed(); len(f) != 1 || f[0] != taken {
		t.Fatalf("Failed = %v, want [%d]", f, taken)
	}
	// Retry while the port is still taken fails again; once it is freed, the relay
	// comes up without a new Sync.
	if err := r.Retry(); err == nil {
		t.Fatal("Retry on a taken port succeeded")
	}
	busy.Close()
	if err := r.Retry(); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, dial(t, taken), []byte("came up"))

	// Closed, the relay stays closed: Retry has nothing to bring back.
	busy2, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy2.Close()
	taken2 := busy2.LocalAddr().(*net.UDPAddr).Port
	_ = r.Sync([]Spec{{Port: taken2, Target: target}})
	r.Close()
	busy2.Close()
	if err := r.Retry(); err != nil || len(r.Failed()) != 0 || len(r.Ports()) != 0 {
		t.Fatalf("after Close: retry err %v, failed %v, running %v", err, r.Failed(), r.Ports())
	}
	if f := r.Failed(); len(f) != 0 {
		t.Fatalf("Failed after recovery = %v", f)
	}
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
