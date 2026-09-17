// Package turnrelay terminates the far end of a vk-turn-proxy tunnel.
//
// Where the mobile network lets through nothing but a whitelist, the TURN servers of a
// whitelisted video-call service still carry media. vk-turn-proxy's client takes TURN
// credentials from a call's invite link, wraps WireGuard's packets in DTLS and has
// the call service's TURN servers relay them to a UDP port on this host. To the network
// that is a call. Here the DTLS is taken off and each packet goes on, as it came, to a
// WireGuard inbound of the local Xray listening on loopback.
//
// The DTLS parameters are the client's, not a choice: it offers one cipher suite, asks
// for the extended master secret and sends a connection id, and a server that differs in
// any of them fails the handshake. It verifies no certificate, so a self-signed one made
// at start is enough. Nothing here authenticates anyone — WireGuard does, one hop in,
// and a peer that cannot complete its handshake reaches nothing but that inbound.
//
// Two client families share the port. vk-turn-proxy sends DTLS as it is; Free Turn
// Proxy seals every datagram as RTP/Opus under a key (mask.go), which is what keeps the
// call service from shaping the relay. The relay tells them apart per datagram.
package turnrelay

import (
	"context"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	dtlsnet "github.com/pion/dtls/v3/pkg/net"
	"github.com/pion/dtls/v3/pkg/protocol"
	"github.com/pion/dtls/v3/pkg/protocol/recordlayer"
	"github.com/pion/transport/v5/udp"
	"golang.org/x/crypto/chacha20poly1305"
)

// Spec is one relay: DTLS on Port, every server address, forwarded to Target.
type Spec struct {
	Port   int    // public UDP port the call service's TURN servers send to
	Target string // host:port of the WireGuard inbound, loopback in practice
	// MaskKey is the Free Turn Proxy masking key, hex of MaskKeyLen bytes. Empty
	// serves unmasked clients only.
	MaskKey string
}

// idleTimeout closes a tunnel leg nothing has crossed for this long, in either direction.
// A client opens several legs and keeps each alive with WireGuard's own keepalive, so a
// leg this quiet belongs to a client that has gone.
const idleTimeout = 30 * time.Minute

// handshakeTimeout bounds one DTLS handshake. The client's own is 20 seconds.
const handshakeTimeout = 30 * time.Second

// maxLegs caps the open legs of one relay. Each costs a socket and two goroutines, and a
// handshake needs no credential, so without a ceiling anyone who finds the port can
// make the box hold as many as they like. A client opens ten by default.
const maxLegs = 4096

// packetSize fits any packet a WireGuard peer with a 1280 MTU sends, with room over.
const packetSize = 1600

// Relay runs the relays asked of it by Sync. A nil Relay runs none.
type Relay struct {
	mu      sync.Mutex
	running map[Spec]*listener
	listen  func(spec Spec) (net.Listener, error)
}

// New returns a relay running nothing.
func New() *Relay {
	return &Relay{running: map[Spec]*listener{}, listen: listenDTLS}
}

// Sync runs exactly the relays in want: new ones start, ones no longer wanted stop, and
// the rest keep their tunnels. A relay that cannot start is reported and the others still
// run — one taken port must not take down every other server's lane.
func (r *Relay) Sync(want []Spec) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for spec, l := range r.running {
		if !slices.Contains(want, spec) {
			l.close()
			delete(r.running, spec)
		}
	}
	var errs []error
	for _, spec := range want {
		if _, ok := r.running[spec]; ok {
			continue
		}
		ln, err := r.listen(spec)
		if err != nil {
			errs = append(errs, fmt.Errorf("turn relay on udp/%d: %w", spec.Port, err))
			continue
		}
		l := &listener{ln: ln, target: spec.Target, done: make(chan struct{})}
		l.ctx, l.cancel = context.WithCancel(context.Background())
		r.running[spec] = l
		go l.serve()
		slog.Info("turn relay: listening", "port", spec.Port, "target", spec.Target)
	}
	return errors.Join(errs...)
}

// Close stops every relay and waits for their tunnels to end.
func (r *Relay) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for spec, l := range r.running {
		l.close()
		delete(r.running, spec)
	}
}

// Ports lists the UDP ports relays are running on, in no particular order.
func (r *Relay) Ports() []int {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int, 0, len(r.running))
	for spec := range r.running {
		out = append(out, spec.Port)
	}
	return out
}

func listenDTLS(spec Spec) (net.Listener, error) {
	var aead cipher.AEAD
	if spec.MaskKey != "" {
		key, err := hex.DecodeString(spec.MaskKey)
		if err != nil || len(key) != MaskKeyLen {
			return nil, fmt.Errorf("masking key is not %d bytes of hex", MaskKeyLen)
		}
		if aead, err = chacha20poly1305.New(key); err != nil {
			return nil, err
		}
	}
	cert, err := selfsign.GenerateSelfSigned()
	if err != nil {
		return nil, err
	}
	// A new client address is admitted only on the start of a DTLS handshake, plain or
	// masked under our key — the filter the DTLS package's own listener applies, with the
	// mask taken into account. Anything else never becomes a connection to track.
	lc := udp.ListenConfig{AcceptFilter: func(packet []byte) bool {
		b := append([]byte(nil), packet...)
		switch p := detectProfile(b); p {
		case profilePlain:
			return isHandshakeRecord(b)
		case profileRTPOpus, profileRTPOpus2, profileRTPOpus3:
			if aead == nil {
				return false
			}
			plain, err := unmask(aead, p, b)
			return err == nil && isHandshakeRecord(plain)
		}
		return false
	}}
	parent, err := lc.Listen("udp", &net.UDPAddr{Port: spec.Port})
	if err != nil {
		return nil, err
	}
	inner := &maskedListener{inner: dtlsnet.PacketListenerFromListener(parent), aead: aead}
	ln, err := dtls.NewListenerWithOptions(inner,
		dtls.WithCertificates(cert),
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256),
		dtls.WithConnectionIDGenerator(dtls.RandomCIDGenerator(8)),
	)
	if err != nil {
		_ = parent.Close()
		return nil, err
	}
	return ln, nil
}

// isHandshakeRecord reports whether a datagram starts with a DTLS handshake record.
func isHandshakeRecord(b []byte) bool {
	pkts, err := recordlayer.UnpackDatagram(b)
	if err != nil || len(pkts) == 0 {
		return false
	}
	h := &recordlayer.Header{}
	return h.Unmarshal(pkts[0]) == nil && h.ContentType == protocol.ContentTypeHandshake
}

// listener is one running relay.
type listener struct {
	ln     net.Listener
	target string
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	legs   sync.WaitGroup
	open   int
	openMu sync.Mutex
}

func (l *listener) close() {
	l.cancel()
	_ = l.ln.Close()
	<-l.done
}

func (l *listener) serve() {
	defer close(l.done)
	defer l.legs.Wait()
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if l.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Debug("turn relay: accept", "err", err)
			continue
		}
		if !l.take() {
			_ = conn.Close()
			continue
		}
		l.legs.Add(1)
		go func() {
			defer l.legs.Done()
			defer l.release()
			defer conn.Close()
			l.leg(conn)
		}()
	}
}

func (l *listener) take() bool {
	l.openMu.Lock()
	defer l.openMu.Unlock()
	if l.open >= maxLegs {
		return false
	}
	l.open++
	return true
}

func (l *listener) release() {
	l.openMu.Lock()
	l.open--
	l.openMu.Unlock()
}

// leg carries one DTLS connection's packets to the target and the target's back. Each
// leg has a socket of its own, so replies return on the leg the packet came from.
func (l *listener) leg(conn net.Conn) {
	if d, ok := conn.(*dtls.Conn); ok {
		ctx, cancel := context.WithTimeout(l.ctx, handshakeTimeout)
		err := d.HandshakeContext(ctx)
		cancel()
		if err != nil {
			slog.Debug("turn relay: handshake", "remote", conn.RemoteAddr(), "err", err)
			return
		}
	}
	upstream, err := net.Dial("udp", l.target)
	if err != nil {
		slog.Warn("turn relay: dial target", "target", l.target, "err", err)
		return
	}
	defer upstream.Close()

	ctx, cancel := context.WithCancel(l.ctx)
	defer cancel()
	// Closed, not deadlined: a pump arms its own read deadline before every read, so a
	// deadline set here is overwritten by whichever pump is between two reads, and that
	// leg then sits until the idle timeout — half an hour of a relay that was asked to
	// stop. A closed socket cannot be reopened by the next loop.
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
		_ = upstream.Close()
	})
	defer stop()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancel()
		// A Free Turn Proxy client greets with its client id first. Nothing checks
		// it — WireGuard authenticates — but it must not reach WireGuard, and a client
		// asking for the stream mode is one this relay cannot serve.
		if !skipGreeting(conn, upstream) {
			return
		}
		pump(upstream, conn)
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		pump(conn, upstream)
	}()
	wg.Wait()
}

// skipGreeting reads a client's first record. Free Turn Proxy's greeting is dropped;
// anything else is tunnel traffic and goes on to dst. false ends the leg.
func skipGreeting(src, dst net.Conn) bool {
	buf := make([]byte, packetSize)
	if err := src.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
		return false
	}
	n, err := src.Read(buf)
	if err != nil {
		return false
	}
	if greeting, stream := isClientIDRecord(buf[:n]); greeting {
		if stream {
			slog.Debug("turn relay: a client asked for stream mode", "remote", src.RemoteAddr())
			return false
		}
		return true
	}
	if err := dst.SetWriteDeadline(time.Now().Add(idleTimeout)); err != nil {
		return false
	}
	_, err = dst.Write(buf[:n])
	return err == nil
}

// pump copies packets from src to dst one read at a time — each read is a whole
// datagram on both kinds of connection — until either side fails or src is idle past
// idleTimeout.
func pump(dst, src net.Conn) {
	buf := make([]byte, packetSize)
	for {
		if err := src.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
			return
		}
		n, err := src.Read(buf)
		if err != nil {
			return
		}
		if err := dst.SetWriteDeadline(time.Now().Add(idleTimeout)); err != nil {
			return
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}
