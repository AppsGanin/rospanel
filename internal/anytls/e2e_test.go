package anytls_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/anytls"
	"github.com/AppsGanin/rospanel/internal/h2fix"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/proxyproto"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// TestAgainstRealXray runs the lane as it runs in the panel: a real Xray TCP-TLS inbound
// whose fallback hands what is not VLESS to the panel's listener (PROXY, then this
// package, then h2fix and the HTTP server with the decoy), and anytls-in behind it. The
// clients are sing-box (RP_SINGBOX_DIRS, with the outbound the panel's sing-box profile
// writes) and mihomo (RP_MIHOMO_BIN, with the Clash profile's proxy). A plain HTTPS
// visitor must still get the decoy. Opt-in: RP_ANYTLS_E2E_DIR holds xray.
func TestAgainstRealXray(t *testing.T) {
	dir := os.Getenv("RP_ANYTLS_E2E_DIR")
	if dir == "" {
		t.Skip("RP_ANYTLS_E2E_DIR not set")
	}
	const uuid, email = "5783a3e7-e373-51cd-8642-c83782b807c5", "u7"
	const n = 32 << 20
	work := t.TempDir()

	// The far end: a download, a stream that never ends (for the cut), a UDP echo.
	target := listenTCP(t)
	go func() {
		_ = http.Serve(target, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/dl":
				w.Header().Set("Content-Length", strconv.Itoa(n))
				_, _ = io.CopyN(w, zeros{}, n)
			case "/forever":
				buf := make([]byte, 16<<10)
				for {
					if _, err := w.Write(buf); err != nil {
						return
					}
					w.(http.Flusher).Flush()
					time.Sleep(10 * time.Millisecond)
				}
			}
		}))
	}()
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { echo.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			k, from, err := echo.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = echo.WriteTo(buf[:k], from)
		}
	}()

	// The panel: the lane in front of the decoy.
	lane, err := anytls.New(xray.AnyTLSInnerAddr)
	if err != nil {
		t.Fatal(err)
	}
	lane.SetUsers("", []anytls.User{{Email: email, UUID: uuid}})
	panelLn := listenTCP(t)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true) // as the panel's: Xray's fallback hands it h2c
	decoy := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "decoy")
	}), ReadHeaderTimeout: 10 * time.Second, Protocols: protocols}
	go func() {
		_ = decoy.Serve(h2fix.Listener{Listener: lane.WrapListener(&proxyproto.Listener{Listener: panelLn})})
	}()
	t.Cleanup(func() { decoy.Close() })

	// Xray: the TCP-TLS inbound with its fallback, anytls-in, stats.
	certPath, keyPath := writeSelfSigned(t, work, "anytls.test")
	vlessPort, apiPort := freePort(t), freePort(t)
	listen, port := xray.InnerListen(xray.AnyTLSInnerAddr)
	cfg := map[string]any{
		"log":    map[string]any{"loglevel": "warning"},
		"api":    map[string]any{"tag": "api", "services": []string{"StatsService"}},
		"stats":  map[string]any{},
		"policy": map[string]any{"system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true}},
		"inbounds": []any{
			map[string]any{"tag": "api", "listen": "127.0.0.1", "port": apiPort, "protocol": "dokodemo-door",
				"settings": map[string]any{"address": "127.0.0.1"}},
			xray.Inbound{
				Tag: xray.TagVLESS, Listen: "127.0.0.1", Port: vlessPort, Protocol: "vless",
				Settings: xray.VLESSInboundSettings{Clients: []xray.VLESSClient{}, Decryption: "none",
					Fallbacks: []xray.Fallback{{Dest: panelLn.Addr().String(), Xver: 1}}},
				StreamSettings: &xray.StreamSettings{Network: "tcp", Security: "tls", TLSSettings: &xray.TLSSettings{
					ALPN:         []string{"h2", "http/1.1"},
					Certificates: []xray.Certificate{{CertificateFile: certPath, KeyFile: keyPath}},
				}},
			},
			xray.Inbound{
				Tag: xray.TagAnyTLS, Listen: listen, Port: port, Protocol: "vless",
				Settings:       xray.VLESSInboundSettings{Clients: []xray.VLESSClient{{ID: uuid, Email: email}}, Decryption: "none"},
				StreamSettings: &xray.StreamSettings{Network: "tcp", Sockopt: json.RawMessage(`{"acceptProxyProtocol":true}`)},
			},
		},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{
			"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
		"routing": map[string]any{"rules": []any{map[string]any{"inboundTag": []string{"api"}, "outboundTag": "api"}}},
	}
	runJSON(t, exec.Command(filepath.Join(dir, "xray"), "run", "-c"), work, "server.json", cfg)
	waitTCP(t, vlessPort)

	// A plain HTTPS visitor still gets the decoy.
	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "anytls.test"}, ForceAttemptHTTP2: true}}
	if resp, err := hc.Get("https://127.0.0.1:" + strconv.Itoa(vlessPort) + "/"); err != nil {
		t.Fatalf("decoy: %v", err)
	} else {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(b) != "decoy" {
			t.Fatalf("decoy answered %q", b)
		}
	}

	set := &model.Settings{Host: "127.0.0.1", SNI: "anytls.test", VLESSPort: vlessPort, AnyTLSEnabled: true}
	u := model.User{ID: 7, UUID: uuid}
	servers := []sub.Server{{Set: set, Access: model.UnrestrictedAccess()}}
	base := "http://" + target.Addr().String()

	// sing-box: the outbound from the panel's profile, insecure only because the test
	// certificate is self-signed.
	for _, sb := range strings.Split(os.Getenv("RP_SINGBOX_DIRS"), ",") {
		if sb == "" {
			continue
		}
		prof, err := sub.SingBoxWithTemplate(u, servers, "", sub.SingBoxCaps{AnyTLS: true})
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Outbounds []map[string]any `json:"outbounds"`
		}
		_ = json.Unmarshal([]byte(prof), &doc)
		var out map[string]any
		for _, o := range doc.Outbounds {
			if o["type"] == "anytls" {
				out = o
			}
		}
		if out == nil {
			t.Fatalf("no anytls outbound in the profile:\n%s", prof)
		}
		out["tls"].(map[string]any)["insecure"] = true
		mixed := freePort(t)
		runJSON(t, exec.Command(filepath.Join(sb, "sing-box"), "run", "-c"), work, "sb.json", map[string]any{
			"log":       map[string]any{"level": "warn"},
			"inbounds":  []any{map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": mixed}},
			"outbounds": []any{out},
		})
		waitTCP(t, mixed)
		viaProxy(t, "sing-box "+filepath.Base(sb), mixed, base, echo.LocalAddr().(*net.UDPAddr), n)
	}

	// mihomo: the Clash profile's proxy.
	if mihomo := os.Getenv("RP_MIHOMO_BIN"); mihomo != "" {
		profile := sub.ClashYAMLFor(u, servers, "clash.meta/v1.19.32")
		var line string
		for _, s := range strings.Split(profile, "\n") {
			if strings.Contains(s, "type: anytls") {
				line = strings.TrimSuffix(s, "}") + ", skip-cert-verify: true}"
			}
		}
		if line == "" {
			t.Fatalf("no anytls proxy in the Clash profile:\n%s", profile)
		}
		mixed := freePort(t)
		conf := fmt.Sprintf("mixed-port: %d\nlog-level: warning\nproxies:\n%s\nrules:\n  - MATCH,%s\n", mixed, line, model.ProtoAnyTLS)
		path := filepath.Join(work, "mihomo.yaml")
		if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
			t.Fatal(err)
		}
		run(t, exec.Command(mihomo, "-d", work, "-f", path))
		waitTCP(t, mixed)
		viaProxy(t, "mihomo", mixed, base, echo.LocalAddr().(*net.UDPAddr), n)

		// Removing the user closes the open transfer.
		pu, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(mixed))
		c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
		resp, err := c.Get(base + "/forever")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body := resp.Body
		done := make(chan time.Time, 1)
		go func() { _, _ = io.Copy(io.Discard, body); done <- time.Now() }()
		time.Sleep(300 * time.Millisecond)
		cut := time.Now()
		lane.SetUsers("", nil)
		select {
		case end := <-done:
			t.Logf("removed user's transfer ended after %v", end.Sub(cut).Round(time.Millisecond))
		case <-time.After(5 * time.Second):
			t.Fatal("the removed user's transfer went on")
		}
	}

	lanes, err := xray.NewSupervisor("", filepath.Join(work, "unused.json"), work).QueryLanesLive("127.0.0.1:" + strconv.Itoa(apiPort))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("lanes: %+v", lanes)
	if lanes[xray.TagAnyTLS].Down < n {
		t.Fatalf("anytls lane %+v, want at least %d down", lanes[xray.TagAnyTLS], n)
	}
	if v := lanes[xray.TagVLESS]; v.Down > n/20 {
		t.Fatalf("vless lane %+v carries AnyTLS's bytes", v)
	}
}

// viaProxy downloads through a local mixed proxy and sends a UDP datagram through its
// SOCKS5 UDP ASSOCIATE.
func viaProxy(t *testing.T, label string, port int, base string, echo *net.UDPAddr, n int) {
	t.Helper()
	pu, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: time.Minute}
	start := time.Now()
	var resp *http.Response
	var err error
	for range 20 {
		if resp, err = c.Get(base + "/dl"); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	got, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got != int64(n) {
		t.Fatalf("%s: downloaded %d of %d", label, got, n)
	}
	t.Logf("%s: %d MiB at %.0f MiB/s", label, n>>20, float64(n)/(1<<20)/time.Since(start).Seconds())
	if err := socksUDP(port, echo, []byte("ping-"+label)); err != nil {
		t.Fatalf("%s UDP: %v", label, err)
	}
	t.Logf("%s: UDP ok", label)
}

// socksUDP does one SOCKS5 UDP ASSOCIATE round trip to echo through the proxy.
func socksUDP(port int, echo *net.UDPAddr, payload []byte) error {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 5*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(c, resp); err != nil {
		return err
	}
	if _, err := c.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	rep := make([]byte, 10)
	if _, err := io.ReadFull(c, rep); err != nil || rep[1] != 0 {
		return fmt.Errorf("associate refused: % x %v", rep, err)
	}
	relay := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(binary.BigEndian.Uint16(rep[8:10]))}
	uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	defer uc.Close()
	dgram := append([]byte{0, 0, 0, 1}, echo.IP.To4()...)
	dgram = binary.BigEndian.AppendUint16(dgram, uint16(echo.Port))
	dgram = append(dgram, payload...)
	buf := make([]byte, 2048)
	for range 20 {
		if _, err := uc.WriteToUDP(dgram, relay); err != nil {
			return err
		}
		_ = uc.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		k, _, err := uc.ReadFromUDP(buf)
		if err == nil && bytes.HasSuffix(buf[:k], payload) {
			return nil
		}
	}
	return fmt.Errorf("no echo")
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func runJSON(t *testing.T, cmd *exec.Cmd, work, name string, cfg any) {
	t.Helper()
	b, _ := json.MarshalIndent(cfg, "", " ")
	path := filepath.Join(work, name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd.Args = append(cmd.Args, path)
	run(t, cmd)
}

func run(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
}

func listenTCP(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

func freePort(t *testing.T) int {
	t.Helper()
	ln := listenTCP(t)
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func waitTCP(t *testing.T, port int) {
	t.Helper()
	for range 100 {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("nothing listens on %d", port)
}

func writeSelfSigned(t *testing.T, dir, name string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	_ = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return certPath, keyPath
}
