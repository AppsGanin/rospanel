package xray

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
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
	"strings"
	"testing"
	"time"
)

// TestSyncHysteriaAgainstRealXray runs the whole thing against a real Xray with real
// Hysteria2 clients: users joining and leaving do not interrupt a user who stays, a
// removed user's open connection stops carrying traffic at once, and one who comes back
// is let through again. Set RP_XRAY_BIN to an Xray binary to run it.
func TestSyncHysteriaAgainstRealXray(t *testing.T) {
	bin := os.Getenv("RP_XRAY_BIN")
	if bin == "" {
		t.Skip("RP_XRAY_BIN is not set")
	}
	dir := t.TempDir()
	certFile, keyFile, pin := selfSignedCert(t, dir)
	apiPort, hyPort := freePort(t, "tcp"), freePort(t, "udp")
	apiAddr := fmt.Sprintf("127.0.0.1:%d", apiPort)

	target := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = target.Serve(ln) }()
	t.Cleanup(func() { _ = target.Close() })
	targetURL := "http://" + ln.Addr().String() + "/"

	users := func(emails ...string) []HysteriaClient {
		var out []HysteriaClient
		for _, e := range emails {
			out = append(out, HysteriaClient{Auth: "pw-" + e, Email: e})
		}
		return out
	}
	hysteria := func(u []HysteriaClient) Inbound {
		return Inbound{
			Tag: "hysteria-in", Listen: "127.0.0.1", Port: hyPort, Protocol: "hysteria",
			Settings: HysteriaInboundSettings{Version: 2, Users: u},
			StreamSettings: &StreamSettings{
				Network: "hysteria", Security: "tls",
				TLSSettings:      &TLSSettings{ALPN: []string{"h3"}, Certificates: []Certificate{{CertificateFile: certFile, KeyFile: keyFile}}},
				HysteriaSettings: &HysteriaSettings{Version: 2},
			},
		}
	}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"api": map[string]any{"tag": "api", "services": []string{"HandlerService", "StatsService", "RoutingService"}},
		"inbounds": []any{
			map[string]any{"tag": "api", "listen": "127.0.0.1", "port": apiPort, "protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1"}},
			hysteria(users("u1", "u2")),
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{
				// Freedom refuses loopback for a proxied client by default; the target is on it.
				"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}},
			map[string]any{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"type": "field", "ruleTag": "rule-0", "inboundTag": []string{"api"}, "outboundTag": "api"},
				map[string]any{"type": "field", "ruleTag": "rule-1", "domain": []string{"full:blocked.test"}, "outboundTag": "block"},
				// A rule into a balancer: its copy must find the balancer the process already has.
				map[string]any{"type": "field", "ruleTag": "rule-2", "network": "tcp,udp", "balancerTag": "out"},
			},
			"balancers": []any{map[string]any{"tag": "out", "selector": []string{"direct"}, "strategy": map[string]any{"type": "random"}}},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sup := NewSupervisor(bin, filepath.Join(dir, "config.json"), dir)
	t.Cleanup(sup.Stop)
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

	fetchers := map[string]*http.Client{}
	for _, email := range []string{"u1", "u2", "u3"} {
		fetchers[email] = hysteriaClient(t, bin, dir, email, hyPort, pin)
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
	waitFor(t, "u1 and u2 to get through", func() bool { return get("u1") == nil && get("u2") == nil })
	if err := get("u3"); err == nil {
		t.Fatal("u3 got through before being added")
	}

	sync := func(emails ...string) {
		t.Helper()
		start := time.Now()
		if err := sup.SyncHysteria(apiAddr, []Inbound{hysteria(users(emails...))}); err != nil {
			t.Fatalf("sync to %v: %v", emails, err)
		}
		t.Logf("sync to %v took %v", emails, time.Since(start))
	}
	// u1 keeps asking all through the changes; no request may stall or fail.
	stop := make(chan struct{})
	stalls := make(chan string, 100)
	go func() {
		for {
			select {
			case <-stop:
				close(stalls)
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

	sync("u1", "u3")
	if err := get("u2"); err == nil {
		t.Error("u2's open connection still carries traffic after u2 was removed")
	}
	waitFor(t, "u3 to get through once added", func() bool { return get("u3") == nil })
	assertRules(t, bin, apiAddr, []string{"live1-cut-0", "live1-0", "live1-1", "live1-2"})

	sync("u1", "u2", "u3")
	waitFor(t, "u2 to get through again once back", func() bool { return get("u2") == nil })
	assertRules(t, bin, apiAddr, []string{"live2-0", "live2-1", "live2-2"})

	sync("u1")
	for _, e := range []string{"u2", "u3"} {
		if err := get(e); err == nil {
			t.Errorf("%s still gets through after removal", e)
		}
	}
	assertRules(t, bin, apiAddr, []string{"live3-cut-0", "live3-0", "live3-1", "live3-2"})

	close(stop)
	for s := range stalls {
		t.Errorf("u1, who stayed throughout, had a request fail or stall: %s", s)
	}
	if pid := runningProc(sup).cmd.Process.Pid; pid != pidBefore {
		t.Errorf("xray was restarted (pid %d → %d)", pidBefore, pid)
	}
}

func assertRules(t *testing.T, bin, apiAddr string, want []string) {
	t.Helper()
	out, err := exec.Command(bin, "api", "lsrules", "--server="+apiAddr).Output()
	if err != nil {
		t.Fatalf("lsrules: %v", err)
	}
	var listed struct {
		Rules []struct {
			RuleTag string `json:"ruleTag"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		t.Fatalf("lsrules: %v\n%s", err, out)
	}
	var got []string
	for _, r := range listed.Rules {
		got = append(got, r.RuleTag)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("running rules %v, want %v", got, want)
	}
}

// hysteriaClient starts an Xray client for one user and returns an HTTP client that
// goes through it.
func hysteriaClient(t *testing.T, bin, dir, email string, hyPort int, pin string) *http.Client {
	t.Helper()
	socks := freePort(t, "tcp")
	cfg := map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"tag": "socks", "listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{"tag": "proxy", "protocol": "hysteria",
			"settings": map[string]any{"address": "127.0.0.1", "port": hyPort, "version": 2},
			"streamSettings": map[string]any{"network": "hysteria", "security": "tls",
				"tlsSettings":      map[string]any{"serverName": "test", "alpn": []string{"h3"}, "pinnedPeerCertSha256": pin},
				"hysteriaSettings": map[string]any{"version": 2, "auth": "pw-" + email}}}},
	}
	path := filepath.Join(dir, "client-"+email+".json")
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
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
}

func freePort(t *testing.T, network string) int {
	t.Helper()
	if network == "udp" {
		c, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).Port
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// selfSignedCert writes a certificate and key and returns their paths with the pin a
// client checks the certificate against.
func selfSignedCert(t *testing.T, dir string) (certFile, keyFile, pin string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, DNSNames: []string{"test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return certFile, keyFile, hex.EncodeToString(sum[:])
}
