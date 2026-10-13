package mieru_test

import (
	"bytes"
	"context"
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
	"testing"
	"time"

	mieruclient "github.com/enfein/mieru/v3/apis/client"
	apicommon "github.com/enfein/mieru/v3/apis/common"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"google.golang.org/protobuf/proto"

	"github.com/AppsGanin/rospanel/internal/mieru"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// TestAgainstRealXray runs the lane between real programs: a mieru client → this
// package → mieru-in (the inbound the panel generates, on its abstract socket on Linux)
// → Xray's freedom outbound → local servers. Clients: mieru's own (in this process),
// and — with RP_MIHOMO_BIN — mihomo with the proxy exactly as the Clash profile writes
// it. Opt-in: RP_MIERU_E2E_DIR holds xray.
func TestAgainstRealXray(t *testing.T) {
	dir := os.Getenv("RP_MIERU_E2E_DIR")
	if dir == "" {
		t.Skip("RP_MIERU_E2E_DIR not set")
	}
	const uuid, email = "5783a3e7-e373-51cd-8642-c83782b807c5", "u7"
	const n = 64 << 20
	work := t.TempDir()

	// The far end: downloads, a stream that never ends (for the cut), a UDP echo.
	target := listenTCP(t)
	go func() {
		_ = http.Serve(target, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/dl":
				size, _ := strconv.ParseInt(r.URL.Query().Get("n"), 10, 64)
				w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
				_, _ = io.CopyN(w, zeros{}, size)
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

	// Xray: mieru-in as the panel generates it, stats, an access log.
	listen, port := xray.InnerListen(xray.MieruInnerAddr)
	apiPort := freePort(t)
	cfg := map[string]any{
		"log":   map[string]any{"loglevel": "warning", "access": filepath.Join(work, "access.log")},
		"api":   map[string]any{"tag": "api", "services": []string{"StatsService"}},
		"stats": map[string]any{},
		"policy": map[string]any{"levels": map[string]any{"0": map[string]any{
			"statsUserUplink": true, "statsUserDownlink": true}},
			"system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true}},
		"inbounds": []any{
			map[string]any{"tag": "api", "listen": "127.0.0.1", "port": apiPort, "protocol": "dokodemo-door",
				"settings": map[string]any{"address": "127.0.0.1"}},
			xray.Inbound{
				Tag: xray.TagMieru, Listen: listen, Port: port, Protocol: "vless",
				Settings:       xray.VLESSInboundSettings{Clients: []xray.VLESSClient{{ID: uuid, Email: email}}, Decryption: "none"},
				StreamSettings: &xray.StreamSettings{Network: "tcp", Sockopt: json.RawMessage(`{"acceptProxyProtocol":true}`)},
			},
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom",
				"settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}},
		},
		"routing": map[string]any{"rules": []any{map[string]any{"inboundTag": []string{"api"}, "outboundTag": "api"}}},
	}
	b, _ := json.MarshalIndent(cfg, "", " ")
	cfgPath := filepath.Join(work, "server.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, exec.Command(filepath.Join(dir, "xray"), "run", "-c", cfgPath))
	waitTCP(t, apiPort)

	for _, transport := range []string{mieru.TCP, mieru.UDP} {
		t.Run(transport, func(t *testing.T) {
			lane := mieru.New(xray.MieruInnerAddr)
			l := mieru.Listen{Port: freePort(t), Transport: transport}
			if err := lane.Apply(l, "", []mieru.User{{Email: email, UUID: uuid}}); err != nil {
				t.Fatal(err)
			}
			defer lane.Close()
			c := client(t, l, email, uuid)
			hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				a, err := net.ResolveTCPAddr("tcp", addr)
				if err != nil {
					return nil, err
				}
				return c.DialContext(ctx, a)
			}}, Timeout: 2 * time.Minute}

			start := time.Now()
			dl, err := hc.Get(fmt.Sprintf("http://%s/dl?n=%d", target.Addr(), n))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.Copy(io.Discard, dl.Body)
			dl.Body.Close()
			if got != n {
				t.Fatalf("downloaded %d of %d", got, n)
			}
			t.Logf("mieru %s download %d MiB: %.0f MiB/s", transport, n>>20, float64(n)/(1<<20)/time.Since(start).Seconds())

			// UDP through the session's UDP ASSOCIATE.
			uc, err := c.DialContext(context.Background(), &net.UDPAddr{IP: net.IPv4zero})
			if err != nil {
				t.Fatal(err)
			}
			pc := apicommon.NewPacketOverStreamTunnel(uc)
			ep := echo.LocalAddr().(*net.UDPAddr)
			dgram := append([]byte{0, 0, 0, 1, 127, 0, 0, 1, byte(ep.Port >> 8), byte(ep.Port)}, "ping"...)
			_ = uc.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := pc.Write(dgram); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 2048)
			k, err := pc.Read(buf)
			if err != nil || !bytes.Equal(buf[:k], dgram) {
				t.Fatalf("UDP echo: % x %v", buf[:k], err)
			}
			uc.Close()
			t.Logf("mieru %s: UDP echo ok", transport)

			// Removing the user ends their open transfer at once.
			forever, err := hc.Get(fmt.Sprintf("http://%s/forever", target.Addr()))
			if err != nil {
				t.Fatal(err)
			}
			defer forever.Body.Close()
			body := forever.Body
			done := make(chan time.Time, 1)
			go func() { _, _ = io.Copy(io.Discard, body); done <- time.Now() }()
			time.Sleep(300 * time.Millisecond)
			cut := time.Now()
			if err := lane.Apply(l, "", nil); err != nil {
				t.Fatal(err)
			}
			select {
			case end := <-done:
				t.Logf("mieru %s: removed user's transfer ended after %v", transport, end.Sub(cut).Round(time.Millisecond))
			case <-time.After(5 * time.Second):
				t.Fatal("the removed user's transfer went on")
			}
		})
	}

	// Xray counted it against the user, on the mieru lane, from the client's address.
	lanes, err := xray.NewSupervisor("", filepath.Join(work, "unused.json"), work).QueryLanesLive("127.0.0.1:" + strconv.Itoa(apiPort))
	if err != nil {
		t.Fatalf("lanes: %v", err)
	}
	if ml := lanes[xray.TagMieru]; ml.Down < 2*n {
		t.Fatalf("mieru lane %+v, want at least %d down", ml, 2*n)
	}
	t.Logf("lanes: %+v", lanes)
	logged, _ := os.ReadFile(filepath.Join(work, "access.log"))
	if !strings.Contains(string(logged), "from 127.0.0.1:") || !strings.Contains(string(logged), "[mieru-in >> direct] email: "+email) {
		t.Fatalf("access log has no mieru-in line for %s from the client:\n%.2000s", email, logged)
	}

	mihomo := os.Getenv("RP_MIHOMO_BIN")
	if mihomo == "" {
		return
	}
	// mihomo with the proxy line the Clash profile hands a mihomo core.
	lane := mieru.New(xray.MieruInnerAddr)
	l := mieru.Listen{Port: freePort(t), Transport: mieru.TCP}
	if err := lane.Apply(l, "", []mieru.User{{Email: email, UUID: uuid}}); err != nil {
		t.Fatal(err)
	}
	defer lane.Close()
	set := &model.Settings{Host: "127.0.0.1", MieruEnabled: true, MieruPort: l.Port, MieruTransport: model.MieruTCP}
	ver, _ := exec.Command(mihomo, "-v").Output()
	ua := "mihomo/" + strings.TrimPrefix(strings.Fields(string(ver) + " x v0")[2], "v")
	profile := sub.ClashYAMLFor(model.User{ID: 7, UUID: uuid}, []sub.Server{{Set: set, Access: model.UnrestrictedAccess()}}, ua)
	var line string
	for _, s := range strings.Split(profile, "\n") {
		if strings.Contains(s, "type: mieru") {
			line = s
		}
	}
	if line == "" {
		t.Fatalf("no mieru proxy for %q:\n%s", ua, profile)
	}
	mixed := freePort(t)
	conf := fmt.Sprintf("mixed-port: %d\nlog-level: warning\nproxies:\n%s\nrules:\n  - MATCH,%s\n",
		mixed, line, model.ProtoMieru)
	confPath := filepath.Join(work, "mihomo.yaml")
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, exec.Command(mihomo, "-d", work, "-f", confPath))
	waitTCP(t, mixed)
	proxyURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(mixed))
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: time.Minute}
	start := time.Now()
	resp, err := hc.Get(fmt.Sprintf("http://%s/dl?n=%d", target.Addr(), n))
	if err != nil {
		t.Fatalf("%s: %v", ua, err)
	}
	defer resp.Body.Close()
	got, _ := io.Copy(io.Discard, resp.Body)
	if got != n {
		t.Fatalf("%s: downloaded %d of %d", ua, got, n)
	}
	t.Logf("%s through the Clash profile's mieru proxy: %d MiB at %.0f MiB/s", ua, n>>20, float64(n)/(1<<20)/time.Since(start).Seconds())
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func client(t *testing.T, l mieru.Listen, name, pass string) mieruclient.Client {
	t.Helper()
	tp := appctlpb.TransportProtocol_TCP
	if l.Transport == mieru.UDP {
		tp = appctlpb.TransportProtocol_UDP
	}
	c := mieruclient.NewClient()
	if err := c.Store(&mieruclient.ClientConfig{Profile: &appctlpb.ClientProfile{
		ProfileName: proto.String("e2e"),
		User:        &appctlpb.User{Name: proto.String(name), Password: proto.String(pass)},
		Servers: []*appctlpb.ServerEndpoint{{
			IpAddress:    proto.String("127.0.0.1"),
			PortBindings: []*appctlpb.PortBinding{{Port: proto.Int32(int32(l.Port)), Protocol: tp.Enum()}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop() })
	return c
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
