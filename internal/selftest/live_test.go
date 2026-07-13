//go:build selftestlive

// Live end-to-end test of the probe against a real Xray. Excluded from the normal
// build (and CI) by the selftestlive tag because it needs a real xray binary and
// internet egress. It proves the actual Go config generator — not a hand-written
// JSON — produces a config a real Xray accepts and passes traffic through.
//
// Run:
//
//	XRAY_BIN=/path/to/xray SELFTEST_CERT=/path/cert.pem SELFTEST_KEY=/path/key.pem \
//	  go test -tags selftestlive -run TestLive ./internal/selftest/ -v
//
// It spins up its own server inbound (using the same cert), then drives the probe.
package selftest

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func liveEnv(t *testing.T) (bin, cert, key string) {
	t.Helper()
	bin = os.Getenv("XRAY_BIN")
	cert = os.Getenv("SELFTEST_CERT")
	key = os.Getenv("SELFTEST_KEY")
	if bin == "" || cert == "" || key == "" {
		t.Skip("set XRAY_BIN, SELFTEST_CERT, SELFTEST_KEY to run the live test")
	}
	return
}

// certPinHex is the hex SHA-256 of the DER cert — the value the probe pins for a
// self-signed server, matching what the share-link's pcs param carries.
func certPinHex(t *testing.T, certPath, keyPath string) string {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load cert: %v", err)
	}
	sum := sha256.Sum256(pair.Certificate[0])
	return hex.EncodeToString(sum[:])
}

// startServer runs an Xray with the given config JSON until the test ends.
func startServer(t *testing.T, bin string, cfg []byte) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "server-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(cfg); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, bin, "run", "-c", f.Name())
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })
	time.Sleep(700 * time.Millisecond) // let it bind
}

func TestLiveVLESS(t *testing.T) {
	bin, cert, key := liveEnv(t)
	const port = 18443
	uuid := "f29f6e30-53d5-4780-a791-00839ae9f954"

	server := fmt.Sprintf(`{
	  "log": {"loglevel":"warning"},
	  "dns": {"servers":["1.1.1.1","8.8.8.8"]},
	  "inbounds": [{
	    "listen":"127.0.0.1","port":%d,"protocol":"vless",
	    "settings":{"clients":[{"id":"%s","flow":"xtls-rprx-vision"}],"decryption":"none"},
	    "streamSettings":{"network":"tcp","security":"tls","tlsSettings":{
	      "alpn":["h2","http/1.1"],"certificates":[{"certificateFile":"%s","keyFile":"%s"}]}}
	  }],
	  "outbounds": [{"protocol":"freedom","settings":{"domainStrategy":"UseIP"}}]
	}`, port, uuid, cert, key)
	startServer(t, bin, []byte(server))

	set := &model.Settings{SNI: "example.com", VLESSPort: port, VLESSEnabled: true,
		TLSPinSHA256: certPinHex(t, cert, key)}
	u := model.User{UUID: uuid}

	res := runOne(context.Background(), bin, protoSpec{model.ProtoVLESS, buildVLESS}, set, u)
	if !res.OK {
		t.Fatalf("VLESS probe failed: %s", res.Detail)
	}
	t.Logf("VLESS ok: %s", res.Detail)
}

func TestLiveHysteria(t *testing.T) {
	bin, cert, key := liveEnv(t)
	const port = 18445
	pw := "hy2pass123"

	server := fmt.Sprintf(`{
	  "log": {"loglevel":"warning"},
	  "dns": {"servers":["1.1.1.1","8.8.8.8"]},
	  "inbounds": [{
	    "listen":"127.0.0.1","port":%d,"protocol":"hysteria",
	    "settings":{"version":2,"users":[{"auth":"%s"}]},
	    "streamSettings":{"network":"hysteria","security":"tls","hysteriaSettings":{"version":2},
	      "tlsSettings":{"alpn":["h3"],"certificates":[{"certificateFile":"%s","keyFile":"%s"}]}}
	  }],
	  "outbounds": [{"protocol":"freedom","settings":{"domainStrategy":"UseIP"}}]
	}`, port, pw, cert, key)
	startServer(t, bin, []byte(server))

	set := &model.Settings{SNI: "example.com", HysteriaPort: port, HysteriaEnabled: true,
		TLSPinSHA256: certPinHex(t, cert, key)}
	u := model.User{Password: pw}

	res := runOne(context.Background(), bin, protoSpec{model.ProtoHysteria, buildHysteria}, set, u)
	if !res.OK {
		t.Fatalf("Hysteria2 probe failed: %s", res.Detail)
	}
	t.Logf("Hysteria2 ok: %s", res.Detail)
}
