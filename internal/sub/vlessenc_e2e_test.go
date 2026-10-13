package sub_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
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
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/extsub"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// TestVLESSEncAgainstRealXray runs a custom VLESS inbound with VLESS Encryption and no
// TLS between real programs: the server is the inbound as the generator writes it, the
// client the outbound the Xray-JSON subscription builds from the inbound's share link,
// and — with RP_MIHOMO_BIN — mihomo with the proxy the Clash profile writes. Opt-in:
// RP_XRAY_BIN names the xray binary.
func TestVLESSEncAgainstRealXray(t *testing.T) {
	bin := os.Getenv("RP_XRAY_BIN")
	if bin == "" {
		t.Skip("RP_XRAY_BIN not set")
	}
	page := listenLocal(t)
	go func() {
		_ = http.Serve(page, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "through the encrypted lane")
		}))
	}()
	target := "http://" + page.Addr().String() + "/"
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555", Password: "p7"}
	set := &model.Settings{Host: "127.0.0.1", SNI: "127.0.0.1", CertPath: "/unused/cert.pem", KeyPath: "/unused/key.pem"} // the lane has no TLS

	for _, tc := range []struct{ name, transport, mode, xmode string }{
		{"tcp-native", model.TrTCP, model.EncNative, ""},
		{"tcp-xorpub", model.TrTCP, model.EncXorPub, ""},
		{"tcp-random", model.TrTCP, model.EncRandom, ""},
		{"xhttp-packet-up", model.TrXHTTP, model.EncNative, model.XHTTPPacketUp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			priv, pub, err := auth.GenerateRealityKeys()
			if err != nil {
				t.Fatal(err)
			}
			dec, enc := model.VLESSEncStrings(tc.mode, priv, pub)
			in := model.Inbound{ID: 3, Enabled: true, Name: "enc", Protocol: model.InbVLESS, Port: freeLocalPort(t),
				Opts: model.InboundOpts{Transport: tc.transport, Security: model.SecNone, Path: "/x", Mode: tc.xmode,
					EncMode: tc.mode, Decryption: dec, Encryption: enc}}
			in.Normalize()
			if err := in.Validate(); err != nil {
				t.Fatal(err)
			}
			cfg, err := xray.Generate(set, []model.User{u}, xray.Options{PanelDest: "127.0.0.1:1", Custom: []model.Inbound{in}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var server any
			for _, ib := range cfg.Inbounds {
				if ib.Tag == in.Tag() {
					server = ib
				}
			}
			runXrayConfig(t, bin, work, "server.json", map[string]any{
				"log":      map[string]any{"loglevel": "warning"},
				"inbounds": []any{server},
				"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{
					"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
			})

			l := link.Custom(u, in, set)
			if !strings.Contains(l, "encryption="+url.QueryEscape(enc)) {
				t.Fatalf("link carries no encryption: %s", l)
			}
			out, ok := extsub.XrayOutbound(l, "proxy")
			if !ok {
				t.Fatalf("the Xray converter refused %s", l)
			}
			socks := freeLocalPort(t)
			runXrayConfig(t, bin, work, "client.json", map[string]any{
				"log":       map[string]any{"loglevel": "warning"},
				"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
				"outbounds": []any{out},
			})
			fetchThrough(t, "socks5://127.0.0.1:"+strconv.Itoa(socks), target)

			mihomo := os.Getenv("RP_MIHOMO_BIN")
			if mihomo == "" || tc.transport != model.TrTCP {
				return
			}
			profile := sub.ClashYAMLFor(u, []sub.Server{{Set: set, Custom: []model.Inbound{in}, Access: model.UnrestrictedAccess()}}, "clash.meta/v1.19.32")
			var line string
			for _, s := range strings.Split(profile, "\n") {
				if strings.Contains(s, "encryption:") {
					line = s
				}
			}
			if line == "" {
				t.Fatalf("no encrypted VLESS proxy in the Clash profile:\n%s", profile)
			}
			mixed := freeLocalPort(t)
			conf := fmt.Sprintf("mixed-port: %d\nlog-level: warning\nproxies:\n%s\nrules:\n  - MATCH,enc\n", mixed, line)
			path := filepath.Join(work, "mihomo.yaml")
			if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
				t.Fatal(err)
			}
			runCmd(t, exec.Command(mihomo, "-d", work, "-f", path))
			fetchThrough(t, "http://127.0.0.1:"+strconv.Itoa(mixed), target)
		})
	}
}

func runXrayConfig(t *testing.T, bin, work, name string, cfg any) {
	t.Helper()
	b, _ := json.MarshalIndent(cfg, "", " ")
	path := filepath.Join(work, name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	runCmd(t, exec.Command(bin, "run", "-c", path))
}

func runCmd(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
}

func fetchThrough(t *testing.T, proxy, target string) {
	t.Helper()
	pu, _ := url.Parse(proxy)
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 5 * time.Second}
	var lastErr error
	for range 40 {
		resp, err := c.Get(target)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(b) == "through the encrypted lane" {
				return
			}
			lastErr = fmt.Errorf("body %q", b)
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("via %s: %v", proxy, lastErr)
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	ln := listenLocal(t)
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

// TestCDNAgainstRealXray runs a VLESS-XHTTP inbound behind a stand-in CDN: a reverse
// proxy ending TLS for the CDN's domain and passing the requests on to the inbound's
// plain port with an X-Forwarded-For of its own. The client is the Xray outbound built
// from the share link, which must reach the inbound through the CDN only; the inbound
// must log the forwarded address. Opt-in: RP_XRAY_BIN.
func TestCDNAgainstRealXray(t *testing.T) {
	bin := os.Getenv("RP_XRAY_BIN")
	if bin == "" {
		t.Skip("RP_XRAY_BIN not set")
	}
	page := listenLocal(t)
	go func() {
		_ = http.Serve(page, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "through the encrypted lane")
		}))
	}()
	work := t.TempDir()
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555"}
	set := &model.Settings{Host: "203.0.113.200", SNI: "203.0.113.200", CertPath: "/unused/cert.pem", KeyPath: "/unused/key.pem"}
	priv, pub, err := auth.GenerateRealityKeys()
	if err != nil {
		t.Fatal(err)
	}
	dec, enc := model.VLESSEncStrings(model.EncNative, priv, pub)

	// The stand-in CDN: TLS for cdn.test on a local port, everything passed on to the
	// inbound, with the client's address as a CDN would put it.
	cdnLn := listenLocal(t)
	origin := freeLocalPort(t)
	cert, pin := selfSignedCert(t, "cdn.test")
	originURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(origin))
	proxy := &httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(originURL)
		r.Out.Host = r.In.Host
		r.Out.Header.Set("X-Forwarded-For", "203.0.113.7")
	}}
	cdn := &http.Server{Handler: proxy, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}}}
	go func() { _ = cdn.ServeTLS(cdnLn, "", "") }()
	t.Cleanup(func() { cdn.Close() })
	cdnPort := cdnLn.Addr().(*net.TCPAddr).Port

	in := model.Inbound{ID: 4, Enabled: true, Name: "cdn", Protocol: model.InbVLESS, Port: origin,
		Opts: model.InboundOpts{Transport: model.TrXHTTP, Security: model.SecNone, Path: "/cdnpath",
			Mode: model.XHTTPPacketUp, EncMode: model.EncNative, Decryption: dec, Encryption: enc,
			CDNHost: "cdn.test", CDNPort: cdnPort}}
	in.Normalize()
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg, err := xray.Generate(set, []model.User{u}, xray.Options{PanelDest: "127.0.0.1:1", Custom: []model.Inbound{in}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var server any
	for _, ib := range cfg.Inbounds {
		if ib.Tag == in.Tag() {
			ib.Listen = "127.0.0.1"
			server = ib
		}
	}
	accessLog := filepath.Join(work, "access.log")
	runXrayConfig(t, bin, work, "server.json", map[string]any{
		"log":      map[string]any{"loglevel": "warning", "access": accessLog},
		"inbounds": []any{server},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{
			"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
	})

	l := link.Custom(u, in, set)
	pl, _ := url.Parse(l)
	if pl.Hostname() != "cdn.test" || pl.Query().Get("security") != "tls" || pl.Query().Get("sni") != "cdn.test" {
		t.Fatalf("the link does not lead through the CDN: %s", l)
	}
	out, ok := extsub.XrayOutbound(l, "proxy")
	if !ok {
		t.Fatalf("the Xray converter refused %s", l)
	}
	// cdn.test is not a real name and its certificate not a public one: point the
	// client at the stand-in and pin its certificate — the rest is the link's own.
	settings := out["settings"].(map[string]any)
	settings["vnext"].([]map[string]any)[0]["address"] = "127.0.0.1"
	stream := out["streamSettings"].(map[string]any)
	tlsSet, _ := stream["tlsSettings"].(map[string]any)
	if tlsSet == nil {
		t.Fatalf("no TLS toward the CDN in %v", stream)
	}
	tlsSet["pinnedPeerCertSha256"] = pin
	socks := freeLocalPort(t)
	runXrayConfig(t, bin, work, "client.json", map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{out},
	})
	fetchThrough(t, "socks5://127.0.0.1:"+strconv.Itoa(socks), "http://"+page.Addr().String()+"/")

	logged, _ := os.ReadFile(accessLog)
	if !strings.Contains(string(logged), "from 203.0.113.7:") {
		t.Fatalf("the inbound did not take the CDN's X-Forwarded-For:\n%s", logged)
	}
}

// selfSignedCert is a certificate for name and its SHA-256 as Xray pins it.
func selfSignedCert(t *testing.T, name string) (tls.Certificate, string) {
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
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(sum[:])
}

// TestPinnedSNIAgainstRealXray runs TLS inbounds whose clients present someone else's
// name as SNI and check the server's certificate by fingerprint (PinCert): VLESS over
// WebSocket and Hysteria2, each through the Xray outbound built from its link and —
// with RP_MIHOMO_BIN — through mihomo with the Clash proxy. Opt-in: RP_XRAY_BIN.
func TestPinnedSNIAgainstRealXray(t *testing.T) {
	bin := os.Getenv("RP_XRAY_BIN")
	if bin == "" {
		t.Skip("RP_XRAY_BIN not set")
	}
	page := listenLocal(t)
	go func() {
		_ = http.Serve(page, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "through the encrypted lane")
		}))
	}()
	work := t.TempDir()
	cert, pin := selfSignedCert(t, "real.test")
	certPath, keyPath := writeCert(t, work, cert)
	set := &model.Settings{Host: "127.0.0.1", SNI: "real.test", CertPath: certPath, KeyPath: keyPath, CertSHA256: pin}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555", Password: "p7-secret"}
	const allowed = "smartcaptcha.yandexcloud.net"

	for _, in := range []model.Inbound{
		{ID: 5, Enabled: true, Name: "pinws", Protocol: model.InbVLESS, Port: freeLocalPort(t),
			Opts: model.InboundOpts{Transport: model.TrWS, Security: model.SecTLS, Path: "/ws", SNI: allowed, PinCert: true}},
		{ID: 6, Enabled: true, Name: "pinhy", Protocol: model.InbHysteria, Port: freeLocalPort(t),
			Opts: model.InboundOpts{SNI: allowed, PinCert: true}},
	} {
		t.Run(in.Protocol, func(t *testing.T) {
			in.Normalize()
			if err := in.Validate(); err != nil {
				t.Fatal(err)
			}
			cfg, err := xray.Generate(set, []model.User{u}, xray.Options{PanelDest: "127.0.0.1:1", Custom: []model.Inbound{in}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var server any
			for _, ib := range cfg.Inbounds {
				if ib.Tag == in.Tag() {
					server = ib
				}
			}
			runXrayConfig(t, bin, work, in.Name+"-server.json", map[string]any{
				"log":      map[string]any{"loglevel": "warning"},
				"inbounds": []any{server},
				"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{
					"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
			})
			l := link.Custom(u, in, set)
			if q, _ := url.Parse(l); q.Query().Get("sni") != allowed || q.Query().Get("pcs") != pin {
				t.Fatalf("the link does not pin: %s", l)
			}
			out, ok := extsub.XrayOutbound(l, "proxy")
			if !ok {
				t.Fatalf("the Xray converter refused %s", l)
			}
			socks := freeLocalPort(t)
			runXrayConfig(t, bin, work, in.Name+"-client.json", map[string]any{
				"log":       map[string]any{"loglevel": "warning"},
				"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
				"outbounds": []any{out},
			})
			fetchThrough(t, "socks5://127.0.0.1:"+strconv.Itoa(socks), "http://"+page.Addr().String()+"/")

			mihomo := os.Getenv("RP_MIHOMO_BIN")
			if mihomo == "" {
				return
			}
			profile := sub.ClashYAMLFor(u, []sub.Server{{Set: set, Custom: []model.Inbound{in}, Access: model.UnrestrictedAccess()}}, "clash.meta/v1.19.32")
			var line string
			for _, s := range strings.Split(profile, "\n") {
				if strings.Contains(s, "fingerprint: ") && strings.Contains(s, in.Name) {
					line = s
				}
			}
			if line == "" {
				t.Fatalf("no pinned proxy in the Clash profile:\n%s", profile)
			}
			mixed := freeLocalPort(t)
			conf := fmt.Sprintf("mixed-port: %d\nlog-level: warning\nproxies:\n%s\nrules:\n  - MATCH,%s\n", mixed, line, in.Name)
			path := filepath.Join(work, in.Name+"-mihomo.yaml")
			if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
				t.Fatal(err)
			}
			runCmd(t, exec.Command(mihomo, "-d", work, "-f", path))
			fetchThrough(t, "http://127.0.0.1:"+strconv.Itoa(mixed), "http://"+page.Addr().String()+"/")
		})
	}
}

// writeCert writes a certificate and its key as PEM files.
func writeCert(t *testing.T, dir string, c tls.Certificate) (string, string) {
	t.Helper()
	keyDER, err := x509.MarshalECPrivateKey(c.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
