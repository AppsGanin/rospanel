package xray

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/turnrelay"
	"github.com/AppsGanin/rospanel/internal/turnrelay/freeturnref/rtpopus"
	"github.com/AppsGanin/rospanel/internal/turnrelay/freeturnref/rtpopus3"
	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
)

// TestSyncWireGuardAgainstRealXray runs the lane end to end against a real Xray: WireGuard
// clients reach the inbound only through the TURN relay's DTLS, as a vk-turn-proxy client
// sends it (the call service's TURN servers, which only move the datagrams, are left
// out). Users joining and leaving do not interrupt a user who stays, a removed user's
// open session stops at once, the one who joins gets through, and Xray is never
// restarted. Set RP_XRAY_BIN to an Xray binary to run it.
func TestSyncWireGuardAgainstRealXray(t *testing.T) {
	bin := os.Getenv("RP_XRAY_BIN")
	if bin == "" {
		t.Skip("RP_XRAY_BIN is not set")
	}
	dir := t.TempDir()
	apiPort, wgPort, relayPort := freePort(t, "tcp"), freePort(t, "udp"), freePort(t, "udp")
	apiAddr := fmt.Sprintf("127.0.0.1:%d", apiPort)

	// Not on loopback: the inbound's userspace network stack drops a packet addressed
	// to 127.0.0.1 as martian, the way a real interface would.
	targetIP := nonLoopbackIPv4(t)
	target := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	ln, err := net.Listen("tcp", net.JoinHostPort(targetIP, "0"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = target.Serve(ln) }()
	t.Cleanup(func() { _ = target.Close() })
	targetURL := "http://" + ln.Addr().String() + "/"

	serverPriv, serverPub, err := awg.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	type identity struct {
		priv string
		slot int
	}
	ids := map[string]identity{}
	for i, email := range []string{"u1", "u2", "u3"} {
		priv, _, err := awg.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		ids[email] = identity{priv, awg.FirstSlot + i}
	}
	peers := func(emails ...string) []WireGuardInboundPeer {
		var out []WireGuardInboundPeer
		for _, e := range emails {
			p, ok := wireGuardPeer(int64(ids[e].slot), ids[e].priv, ids[e].slot)
			if !ok {
				t.Fatalf("no peer for %s", e)
			}
			p.Email = e
			out = append(out, p)
		}
		return out
	}
	inbound := func(p []WireGuardInboundPeer) Inbound {
		return Inbound{
			Tag: "wg-in", Listen: "127.0.0.1", Port: wgPort, Protocol: model.InbWireGuard,
			Settings: WireGuardInboundSettings{SecretKey: serverPriv, Address: []string{awg.ServerAddr.String()}, MTU: WireGuardTURNMTU, Peers: p},
		}
	}
	cfg := map[string]any{
		"log":   map[string]any{"loglevel": "warning"},
		"api":   map[string]any{"tag": "api", "services": []string{"HandlerService", "StatsService"}},
		"stats": map[string]any{},
		"policy": map[string]any{"levels": map[string]any{"0": map[string]any{
			"statsUserUplink": true, "statsUserDownlink": true,
		}}},
		"inbounds": []any{
			map[string]any{"tag": "api", "listen": "127.0.0.1", "port": apiPort, "protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1"}},
			inbound(peers("u1", "u2")),
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{
				// Freedom refuses private addresses for a proxied client by default; the
				// target is on one.
				"finalRules": []any{map[string]any{"action": "allow", "ip": []string{targetIP + "/32"}}}}},
		},
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api"},
		}},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sup := NewSupervisor(bin, filepath.Join(dir, "config.json"), dir)
	t.Cleanup(sup.Stop)
	// Who the access log says was here: the inbound logs no email, the tap names the user.
	var seenMu sync.Mutex
	seen := map[string]string{} // email → address
	sup.SetOnAccess(func(email, ip, dest string) {
		seenMu.Lock()
		seen[email] = ip
		seenMu.Unlock()
	})
	sighted := func(email string) string {
		seenMu.Lock()
		defer seenMu.Unlock()
		return seen[email]
	}
	if err := sup.ApplyRaw(data); err != nil {
		t.Fatalf("start xray: %v", err)
	}
	waitFor(t, "xray to serve its api", func() bool {
		c, err := net.Dial("tcp", apiAddr)
		if err == nil {
			c.Close()
		}
		return err == nil
	})
	pidBefore := runningProc(sup).cmd.Process.Pid

	maskKey := make([]byte, turnrelay.MaskKeyLen)
	if _, err := cryptorand.Read(maskKey); err != nil {
		t.Fatal(err)
	}
	relay := turnrelay.New()
	t.Cleanup(relay.Close)
	if err := relay.Sync([]turnrelay.Spec{{
		Port: relayPort, Target: net.JoinHostPort("127.0.0.1", strconv.Itoa(wgPort)), MaskKey: hex.EncodeToString(maskKey),
	}}); err != nil {
		t.Fatal(err)
	}

	// u1 and u3 come as Free Turn Proxy clients, masked (rtpopus3, rtpopus) and greeting
	// first; u2 as a plain vk-turn-proxy client. One relay, one port.
	masks := map[string]func() (maskCodec, error){
		"u1": func() (maskCodec, error) { return rtpopus3.NewConn(maskKey, false) },
		"u3": func() (maskCodec, error) { return rtpopus.NewConn(maskKey, false) },
	}
	fetchers := map[string]*http.Client{}
	for _, email := range []string{"u1", "u2", "u3"} {
		var codec maskCodec
		if mk := masks[email]; mk != nil {
			c, err := mk()
			if err != nil {
				t.Fatal(err)
			}
			codec = c
		}
		shim := turnClientShim(t, relayPort, codec)
		fetchers[email] = wireGuardClient(t, bin, dir, email, ids[email].priv, ids[email].slot, serverPub, shim)
	}
	get := func(email string) error {
		resp, err := fetchers[email].Get(targetURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err == nil && string(body) != "ok" {
			err = fmt.Errorf("body %q", body)
		}
		return err
	}
	waitLong(t, "u1 and u2 to get through the relay", func() bool { return get("u1") == nil && get("u2") == nil })
	if err := get("u3"); err == nil {
		t.Fatal("u3 got through before being added")
	}

	// u1 keeps asking all through the changes; no request may fail or stall.
	stop := make(chan struct{})
	stalls := make(chan string, 100)
	go func() {
		defer close(stalls)
		for {
			select {
			case <-stop:
				return
			default:
			}
			start := time.Now()
			if err := get("u1"); err != nil || time.Since(start) > 2*time.Second {
				stalls <- fmt.Sprintf("after %v: %v", time.Since(start), err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	sync := func(emails ...string) {
		t.Helper()
		if err := sup.SyncWireGuard(apiAddr, []Inbound{inbound(peers(emails...))}); err != nil {
			t.Fatalf("sync to %v: %v", emails, err)
		}
	}
	sync("u1", "u3")
	// u2's session was up a moment ago; with the peer gone it carries nothing.
	if err := get("u2"); err == nil {
		t.Error("u2's open session still carries traffic after u2 was removed")
	}
	waitLong(t, "u3 to get through once added", func() bool { return get("u3") == nil })
	// Attributed by tunnel address, and the lookup followed the live change.
	if ip := sighted("u1"); ip != "10.66.0.2" {
		t.Errorf("u1 seen from %q in the access log, want its tunnel address 10.66.0.2", ip)
	}
	waitFor(t, "u3's connections to be attributed", func() bool { return sighted("u3") == "10.66.0.4" })

	sync("u1", "u2", "u3")
	waitLong(t, "u2 to get through again once back", func() bool { return get("u2") == nil })

	close(stop)
	for s := range stalls {
		t.Errorf("u1, who stayed throughout, had a request fail or stall: %s", s)
	}

	// Traffic is counted per user like any lane's: the peer's email is the user.
	out, err := exec.Command(bin, "api", "statsquery", "--server="+apiAddr, "-pattern", "user>>>u1>>>traffic").Output()
	if err != nil {
		t.Fatalf("statsquery: %v", err)
	}
	if !strings.Contains(string(out), "user>>>u1>>>traffic>>>downlink") {
		t.Errorf("no per-user traffic counter for u1: %s", out)
	}
	if pid := runningProc(sup).cmd.Process.Pid; pid != pidBefore {
		t.Errorf("xray was restarted (pid %d → %d)", pidBefore, pid)
	}
}

// nonLoopbackIPv4 is an address of this machine off loopback, for a target a proxied
// client may reach.
func nonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	t.Skip("no IPv4 address off loopback to serve the target on")
	return ""
}

// waitLong is waitFor with room for WireGuard's handshake retries (five seconds apart).
func waitLong(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// maskCodec is a Free Turn Proxy client codec (turnrelay/freeturnref).
type maskCodec interface {
	WrapInto(dst, payload []byte) (int, error)
	Unwrap(wire, dst []byte) (int, error)
	Overhead() int
}

// maskedPacketConn seals and opens datagrams with a Free Turn Proxy codec.
type maskedPacketConn struct {
	net.PacketConn
	codec maskCodec
	mu    sync.Mutex
}

func (c *maskedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	buf := make([]byte, 2048)
	for {
		n, addr, err := c.PacketConn.ReadFrom(buf)
		if err != nil {
			return 0, addr, err
		}
		if m, err := c.codec.Unwrap(buf[:n], p); err == nil {
			return m, addr, nil
		}
	}
}

func (c *maskedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
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

// turnClientShim stands in for a TURN client on the user's device: a local UDP port the
// WireGuard app sends to, carried to the relay over DTLS with vk-turn-proxy's options —
// masked and greeting first like Free Turn Proxy when a codec is given. It returns the
// local address.
func turnClientShim(t *testing.T, relayPort int, codec maskCodec) string {
	t.Helper()
	local, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	cert, err := selfsign.GenerateSelfSigned()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pc := net.PacketConn(raw)
	if codec != nil {
		pc = &maskedPacketConn{PacketConn: raw, codec: codec}
	}
	conn, err := dtls.ClientWithOptions(pc, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: relayPort},
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
		t.Fatalf("dtls handshake with the relay: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if codec != nil {
		// Free Turn Proxy's greeting: its client id and the datagram mode.
		id := "0123456789abcdef0123456789abcdef"
		if _, err := conn.Write(append(append([]byte{byte(len(id))}, id...), 1)); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	var app net.Addr
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := local.ReadFrom(buf)
			if err != nil {
				return
			}
			mu.Lock()
			app = from
			mu.Unlock()
			if _, err := conn.Write(buf[:n]); err != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			mu.Lock()
			to := app
			mu.Unlock()
			if to != nil {
				_, _ = local.WriteTo(buf[:n], to)
			}
		}
	}()
	return local.LocalAddr().String()
}

// wireGuardClient starts an Xray WireGuard client for one user, its endpoint the shim,
// and returns an HTTP client that goes through it.
func wireGuardClient(t *testing.T, bin, dir, email, priv string, slot int, serverPub, endpoint string) *http.Client {
	t.Helper()
	addr, _ := awg.ClientAddr(slot)
	socks := freePort(t, "tcp")
	cfg := map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"tag": "socks", "listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{"tag": "proxy", "protocol": "wireguard", "settings": map[string]any{
			"secretKey":   priv,
			"address":     []string{addr.String() + "/32"},
			"mtu":         WireGuardTURNMTU,
			"noKernelTun": true,
			"peers":       []any{map[string]any{"publicKey": serverPub, "endpoint": endpoint, "keepAlive": 5}},
		}}},
	}
	path := filepath.Join(dir, "wg-client-"+email+".json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socks))
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
}
