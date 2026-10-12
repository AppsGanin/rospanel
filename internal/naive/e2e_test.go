package naive_test

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/naive"
	"github.com/AppsGanin/rospanel/internal/proxyproto"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// TestAgainstRealXrayAndNaive runs the lane between the real programs: the official
// NaiveProxy client → Xray's VLESS-TLS inbound → fallback → this package → naive-in →
// Xray's freedom outbound → a local server. Opt-in: RP_NAIVE_E2E_DIR holds xray, naive,
// and cert.pem/key.pem for naive.test signed by a CA the system trusts (the client has
// no way to skip verification). RP_NAIVE_E2E_MB sizes the transfers.
func TestAgainstRealXrayAndNaive(t *testing.T) {
	dir := os.Getenv("RP_NAIVE_E2E_DIR")
	if dir == "" {
		t.Skip("RP_NAIVE_E2E_DIR not set")
	}
	mb := 200
	if v, err := strconv.Atoi(os.Getenv("RP_NAIVE_E2E_MB")); err == nil && v > 0 {
		mb = v
	}
	const uuid, email = "5783a3e7-e373-51cd-8642-c83782b807c5", "u7"
	work := t.TempDir()

	// The far end: a download of n bytes, an upload that reports what it got, and a
	// stream that never ends (for the cut).
	target := listen(t)
	far := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dl":
			n, _ := strconv.ParseInt(r.URL.Query().Get("n"), 10, 64)
			w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
			_, _ = io.CopyN(w, zeros{}, n)
		case "/ul":
			n, _ := io.Copy(io.Discard, r.Body)
			fmt.Fprint(w, n)
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
	})
	go func() { _ = http.Serve(target, far) }()

	// The panel: the lane in front of a decoy, h2c behind the PROXY listener.
	lane := naive.New(xray.NaiveInnerAddr)
	lane.SetUsers("", []naive.User{{Email: email, UUID: uuid}})
	panelLn := listen(t)
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	decoy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "Caddy")
		http.Error(w, "decoy", http.StatusNotFound)
	})
	panel := &http.Server{Handler: lane.Wrap(decoy), Protocols: p, HTTP2: naive.HTTP2Config()}
	go func() { _ = panel.Serve(&proxyproto.Listener{Listener: panelLn}) }()
	t.Cleanup(func() { panel.Close() })

	// Xray: the :443-style inbound with the fallback, naive-in, stats, an access log.
	// RP_NAIVE_E2E_VLESS_OFF runs it as the panel does with VLESS-TCP-TLS switched off:
	// the listener and its fallback stay, holding no VLESS user.
	vlessOff := os.Getenv("RP_NAIVE_E2E_VLESS_OFF") != ""
	vlessClients := []xray.VLESSClient{{ID: uuid, Flow: xray.VisionFlow, Email: email}}
	if vlessOff {
		vlessClients = []xray.VLESSClient{}
	}
	vlessPort, apiPort := freePort(t), freePort(t)
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
				Tag: xray.TagVLESS, Listen: "127.0.0.1", Port: vlessPort, Protocol: "vless",
				Settings: xray.VLESSInboundSettings{
					Clients:    vlessClients,
					Decryption: "none",
					Fallbacks:  []xray.Fallback{{Dest: panelLn.Addr().String(), Xver: 1}},
				},
				StreamSettings: &xray.StreamSettings{Network: "tcp", Security: "tls", TLSSettings: &xray.TLSSettings{
					ALPN:         []string{"h2", "http/1.1"},
					Certificates: []xray.Certificate{{CertificateFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem")}},
				}},
			},
			naiveInInbound(t, uuid, email),
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom",
				"settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}},
		},
		"routing": map[string]any{"rules": []any{map[string]any{"inboundTag": []string{"api"}, "outboundTag": "api"}}},
	}
	server := runXray(t, dir, work, "server.json", cfg)
	waitPort(t, vlessPort)

	// The NaiveProxy client, as a SOCKS proxy.
	naivePort := freePort(t)
	naiveLog := filepath.Join(work, "naive.log")
	cmd := exec.Command(filepath.Join(dir, "naive"),
		"--listen=socks://127.0.0.1:"+strconv.Itoa(naivePort),
		fmt.Sprintf("--proxy=https://%s:%s@naive.test:%d", email, uuid, vlessPort),
		"--host-resolver-rules=MAP naive.test 127.0.0.1",
		"--log="+naiveLog)
	start(t, cmd)
	waitPort(t, naivePort)
	client := cmd.Process.Pid

	base := "http://" + target.Addr().String()
	viaNaive := socksClient(naivePort)
	n := int64(mb) << 20

	var dl, ul time.Duration
	cpu := measureCPU(server, client, func() { dl = timed(func() { mustDownload(t, viaNaive, base, n) }) })
	logRun(t, "naive download", n, dl, cpu)
	cpu = measureCPU(server, client, func() { ul = timed(func() { mustUpload(t, viaNaive, base, n/2) }) })
	logRun(t, "naive upload", n/2, ul, cpu)

	// Xray counted the tunnel against the user, both ways.
	up, down := userStats(t, dir, apiPort, email)
	t.Logf("xray counters for %s: up %d, down %d", email, up, down)
	// The per-protocol split as the panel reads it: NaiveProxy on its lane, its bytes
	// taken off the TCP-TLS inbound whose fallback carried them, and the counters zeroed
	// by the read so the next one starts from nothing.
	lanesAPI := xray.NewSupervisor("", filepath.Join(work, "unused.json"), work)
	lanes, err := lanesAPI.QueryLanesLive("127.0.0.1:" + strconv.Itoa(apiPort))
	if err != nil {
		t.Fatalf("lanes: %v", err)
	}
	t.Logf("lanes after the naive transfers: %+v", lanes)
	if nl := lanes[xray.TagNaive]; nl.Down < n || nl.Up < n/2 {
		t.Fatalf("naive lane %+v, want at least %d down / %d up", nl, n, n/2)
	}
	if vl := lanes[xray.TagVLESS]; vl.Down > n/50 {
		t.Fatalf("VLESS lane kept NaiveProxy's bytes: %+v", vl)
	}
	if again, err := lanesAPI.QueryLanesLive("127.0.0.1:" + strconv.Itoa(apiPort)); err != nil || again[xray.TagNaive].Down > n/50 {
		t.Fatalf("the read did not zero the counters: %+v %v", again, err)
	}
	if down < n || up < n/2 {
		t.Fatalf("xray counted up %d down %d, want at least %d / %d", up, down, n/2, n)
	}
	logged, _ := os.ReadFile(filepath.Join(work, "access.log"))
	if !strings.Contains(string(logged), "[naive-in ") || !strings.Contains(string(logged), "email: "+email) {
		t.Fatalf("access log has no naive-in line for %s:\n%.2000s", email, logged)
	}
	nlog, _ := os.ReadFile(naiveLog)
	if !strings.Contains(string(nlog), "padding type: Variant1") && !strings.Contains(string(nlog), "padding type: 1") {
		t.Logf("naive log does not name the padding type:\n%.3000s", nlog)
	}

	// A CONNECT with the wrong credentials, straight over TLS, is the decoy's.
	for name, auth := range map[string]string{"none": "", "wrong": email + ":00000000-0000-0000-0000-000000000000"} {
		if code, server := probe(t, vlessPort, auth); code != http.StatusNotFound || server != "Caddy" {
			t.Fatalf("probe %s: %d server %q, want the decoy's 404", name, code, server)
		}
	}

	// Removing the user ends a transfer in progress.
	resp, err := viaNaive.Get(base + "/forever") //nolint:bodyclose // closed by the defer below
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadFull(resp.Body, make([]byte, 64<<10)); err != nil {
		t.Fatalf("forever stream: %v", err)
	}
	cut := time.Now()
	lane.SetUsers("", nil)
	errc := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, resp.Body); errc <- err }()
	select {
	case err := <-errc:
		t.Logf("removed user's transfer ended after %v (%v)", time.Since(cut).Round(time.Millisecond), err)
	case <-time.After(10 * time.Second):
		t.Fatal("removed user's transfer still running after 10s")
	}
	resp.Body.Close()
	lane.SetUsers("", []naive.User{{Email: email, UUID: uuid}})

	// sing-box's naive outbound (cronet), from the profile the panel generates for it.
	for _, sb := range strings.Split(os.Getenv("RP_SINGBOX_DIRS"), ",") {
		if sb != "" {
			singboxThroughLane(t, sb, work, vlessPort, apiPort, dir, email, uuid, base)
		}
	}

	if vlessOff {
		return
	}
	// The same transfers over VLESS-Vision through the same Xray, for scale.
	vlessSocks := freePort(t)
	vclient := runXray(t, dir, work, "client.json", map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": vlessSocks, "protocol": "socks",
			"settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{"protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": vlessPort,
				"users": []any{map[string]any{"id": uuid, "flow": xray.VisionFlow, "encryption": "none"}}}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "tls",
				"tlsSettings": map[string]any{"serverName": "naive.test", "fingerprint": "chrome"}}}},
	})
	waitPort(t, vlessSocks)
	viaVLESS := socksClient(vlessSocks)
	cpu = measureCPU(server, vclient, func() { dl = timed(func() { mustDownload(t, viaVLESS, base, n) }) })
	logRun(t, "vision download", n, dl, cpu)
	cpu = measureCPU(server, vclient, func() { ul = timed(func() { mustUpload(t, viaVLESS, base, n/2) }) })
	logRun(t, "vision upload", n/2, ul, cpu)
}

// naiveInInbound is naive-in as the generator writes it (pinned by xray's
// TestNaiveInboundShape).
func naiveInInbound(t *testing.T, uuid, email string) any {
	t.Helper()
	listen, port := xray.InnerListen(xray.NaiveInnerAddr)
	return xray.Inbound{
		Tag: xray.TagNaive, Listen: listen, Port: port, Protocol: "vless",
		Settings:       xray.VLESSInboundSettings{Clients: []xray.VLESSClient{{ID: uuid, Email: email}}, Decryption: "none"},
		StreamSettings: &xray.StreamSettings{Network: "tcp", Sockopt: json.RawMessage(`{"acceptProxyProtocol":true}`)},
		Sniffing:       &xray.Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}},
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

func freePort(t *testing.T) int {
	ln := listen(t)
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func waitPort(t *testing.T, port int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %d never opened", port)
}

func runXray(t *testing.T, dir, work, name string, cfg any) int {
	t.Helper()
	b, _ := json.MarshalIndent(cfg, "", " ")
	path := filepath.Join(work, name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(dir, "xray"), "run", "-c", path)
	start(t, cmd)
	return cmd.Process.Pid
}

// cpuSplit is CPU seconds spent by the Xray server, this process (the lane, the target
// server and the test's HTTP client) and the proxy client.
type cpuSplit [3]float64

func logRun(t *testing.T, label string, n int64, d time.Duration, c cpuSplit) {
	g := float64(n) / (1 << 30)
	t.Logf("%-15s %5d MiB %5.0f MiB/s   CPU s/GiB: xray %.2f  lane+harness %.2f  client %.2f",
		label, n>>20, mbps(n, d), c[0]/g, c[1]/g, c[2]/g)
}

func measureCPU(server, client int, f func()) cpuSplit {
	pids := []int{server, os.Getpid(), client}
	var before cpuSplit
	for i, p := range pids {
		before[i] = cpuSeconds(p)
	}
	f()
	var out cpuSplit
	for i, p := range pids {
		out[i] = cpuSeconds(p) - before[i]
	}
	return out
}

// cpuSeconds is a process's user+system time from /proc (0 where there is none).
func cpuSeconds(pid int) float64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+2:])
	ut, _ := strconv.ParseFloat(f[11], 64)
	st, _ := strconv.ParseFloat(f[12], 64)
	return (ut + st) / 100 // USER_HZ
}

func start(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
}

func socksClient(port int) *http.Client {
	u, _ := url.Parse("socks5://127.0.0.1:" + strconv.Itoa(port))
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u), DisableCompression: true}}
}

func timed(f func()) time.Duration {
	t0 := time.Now()
	f()
	return time.Since(t0)
}

func mbps(n int64, d time.Duration) float64 { return float64(n) / (1 << 20) / d.Seconds() }

func mustDownload(t *testing.T, c *http.Client, base string, n int64) {
	t.Helper()
	resp, err := c.Get(base + "/dl?n=" + strconv.FormatInt(n, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.Copy(io.Discard, resp.Body)
	if err != nil || got != n {
		t.Fatalf("download: %d of %d bytes, %v", got, n, err)
	}
}

func mustUpload(t *testing.T, c *http.Client, base string, n int64) {
	t.Helper()
	resp, err := c.Post(base+"/ul", "application/octet-stream", io.LimitReader(zeros{}, n))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != strconv.FormatInt(n, 10) {
		t.Fatalf("upload: server got %s of %d", b, n)
	}
}

func userStats(t *testing.T, dir string, apiPort int, email string) (up, down int64) {
	t.Helper()
	out, err := exec.Command(filepath.Join(dir, "xray"), "api", "statsquery",
		"--server=127.0.0.1:"+strconv.Itoa(apiPort), "-pattern", "user>>>"+email).Output()
	if err != nil {
		t.Fatalf("statsquery: %v", err)
	}
	var res struct {
		Stat []struct {
			Name  string `json:"name"`
			Value int64  `json:"value,string"`
		} `json:"stat"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		// Older CLIs print value as a number.
		var res2 struct {
			Stat []struct {
				Name  string `json:"name"`
				Value int64  `json:"value"`
			} `json:"stat"`
		}
		if err2 := json.Unmarshal(out, &res2); err2 != nil {
			t.Fatalf("statsquery output %s: %v", out, err)
		}
		for _, s := range res2.Stat {
			res.Stat = append(res.Stat, struct {
				Name  string `json:"name"`
				Value int64  `json:"value,string"`
			}{s.Name, s.Value})
		}
	}
	for _, s := range res.Stat {
		switch {
		case strings.HasSuffix(s.Name, "uplink"):
			up = s.Value
		case strings.HasSuffix(s.Name, "downlink"):
			down = s.Value
		}
	}
	return up, down
}

// probe sends a CONNECT over TLS + HTTP/2 the way a scanner would, and returns the status
// and Server header it got.
func probe(t *testing.T, port int, auth string) (int, string) {
	t.Helper()
	tr := &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{ServerName: "naive.test"},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(port))
		},
	}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequest(http.MethodConnect, "https://naive.test:"+strconv.Itoa(port), http.NoBody)
	req.Host = "example.com:443"
	if auth != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth)))
	}
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("probe spoke HTTP/%d", resp.ProtoMajor)
	}
	return resp.StatusCode, resp.Header.Get("Server")
}

// singboxThroughLane checks the panel's sing-box profile against a real sing-box: the
// whole profile must load, and its naive outbound must carry traffic through the lane.
func singboxThroughLane(t *testing.T, sbDir, work string, vlessPort, apiPort int, xrayDir, email, uuid, base string) {
	t.Helper()
	set := &model.Settings{Host: "127.0.0.1", SNI: "naive.test", VLESSPort: vlessPort, NaiveEnabled: true}
	u := model.User{ID: 7, UUID: uuid}
	servers := []sub.Server{{Set: set, Access: model.UnrestrictedAccess()}}
	prof, err := sub.SingBoxWithTemplate(u, servers, "", true)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(sbDir, "sing-box")
	ver, _ := exec.Command(bin, "version").Output()
	name := strings.Fields(string(ver) + " ? ? ?")[2]
	path := filepath.Join(work, "sb-profile-"+name+".json")
	if err := os.WriteFile(path, []byte(prof), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box %s refuses the profile: %v\n%s\n%s", name, err, out, prof)
	}
	if plain, _ := sub.SingBoxWithTemplate(u, servers, "", false); strings.Contains(plain, `"naive"`) {
		t.Fatal("a client without naive support was given a naive outbound")
	}

	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(prof), &doc); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	for _, o := range doc.Outbounds {
		if o["type"] == "naive" {
			out = o
		}
	}
	if out == nil {
		t.Fatalf("no naive outbound in the profile:\n%s", prof)
	}
	socks := freePort(t)
	cfg := map[string]any{
		"log":       map[string]any{"level": "warn"},
		"inbounds":  []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": socks}},
		"outbounds": []any{out},
		"route":     map[string]any{"final": out["tag"]},
	}
	b, _ := json.Marshal(cfg)
	run := filepath.Join(work, "sb-run-"+name+".json")
	if err := os.WriteFile(run, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", run)
	start(t, cmd)
	waitPort(t, socks)
	_, before := userStats(t, xrayDir, apiPort, email)
	n := int64(100 << 20)
	c := socksClient(socks)
	d := timed(func() { mustDownload(t, c, base, n) })
	mustUpload(t, c, base, n/4)
	_, after := userStats(t, xrayDir, apiPort, email)
	if after-before < n {
		t.Fatalf("sing-box %s: xray counted %d bytes down, want at least %d", name, after-before, n)
	}
	t.Logf("sing-box %s naive outbound: download %d MiB %.0f MiB/s, upload ok", name, n>>20, mbps(n, d))

	// UDP over TCP: two echo servers, so the stream carries more than one destination.
	for i := range 2 {
		echo := udpEcho(t)
		got := socksUDPRoundTrip(t, socks, echo, []byte(fmt.Sprintf("ping-%d via %s", i, name)))
		if string(got) != fmt.Sprintf("ping-%d via %s", i, name) {
			t.Fatalf("sing-box %s: UDP echo %q", name, got)
		}
	}
	logged, _ := os.ReadFile(filepath.Join(work, "access.log"))
	if !regexp.MustCompile(`accepted udp:127\.0\.0\.1:\d+ \[naive-in [-=]?>+ direct\] email: ` + email).Match(logged) {
		t.Fatalf("sing-box %s: no UDP line on naive-in for %s in the access log:\n%s", name, email, logged)
	}
	t.Logf("sing-box %s naive outbound: UDP over TCP ok", name)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// udpEcho is a UDP echo server on loopback.
func udpEcho(t *testing.T) *net.UDPAddr {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteToUDP(buf[:n], from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr)
}

// socksUDPRoundTrip sends one datagram to dst through a SOCKS5 UDP ASSOCIATE and returns
// the reply.
func socksUDPRoundTrip(t *testing.T, socksPort int, dst *net.UDPAddr, payload []byte) []byte {
	t.Helper()
	ctrl, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(socksPort))
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	_ = ctrl.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := ctrl.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(ctrl, make([]byte, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	rep := make([]byte, 10)
	if _, err := io.ReadFull(ctrl, rep); err != nil || rep[1] != 0 || rep[3] != 1 {
		t.Fatalf("UDP ASSOCIATE: % x %v", rep, err)
	}
	relay := &net.UDPAddr{IP: net.IP(rep[4:8]), Port: int(rep[8])<<8 | int(rep[9])}
	if relay.IP.IsUnspecified() {
		relay.IP = net.IPv4(127, 0, 0, 1)
	}
	uc, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	hdr := append([]byte{0, 0, 0, 1}, dst.IP.To4()...)
	hdr = append(hdr, byte(dst.Port>>8), byte(dst.Port))
	buf := make([]byte, 2048)
	for range 5 { // UDP: retry a lost first packet while the tunnel comes up
		if _, err := uc.Write(append(hdr, payload...)); err != nil {
			t.Fatal(err)
		}
		_ = uc.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := uc.Read(buf)
		if err == nil && n > len(hdr) {
			return buf[len(hdr):n]
		}
	}
	t.Fatalf("no UDP reply from %v through the tunnel", dst)
	return nil
}
