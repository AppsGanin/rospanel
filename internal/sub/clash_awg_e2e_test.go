package sub_test

import (
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

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// TestClashAWGAgainstRealMihomo runs the AmneziaWG proxy the Clash profile carries in a
// real mihomo against the panel's own tunnel (amneziawg-go, the 3.1 parameters the panel
// generates), and fetches a page served on the server's tunnel address through it.
// Opt-in: RP_MIHOMO_BIN names the mihomo binary; it needs Linux, /dev/net/tun and
// CAP_NET_ADMIN for the server's TUN.
func TestClashAWGAgainstRealMihomo(t *testing.T) {
	bin := os.Getenv("RP_MIHOMO_BIN")
	if bin == "" {
		t.Skip("RP_MIHOMO_BIN not set")
	}
	serverPriv, serverPub, err := awg.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	clientPriv, clientPub, err := awg.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	params := awg.RandomParams()
	if !params.NeedsAgent31() {
		t.Fatal("the generated parameters are not 3.1")
	}
	const slot = 7
	addr, _ := awg.ClientAddr(slot)
	port := freeUDPPort(t)

	dev := awg.New()
	if err := dev.Apply(awg.Config{PrivateKey: serverPriv, ListenPort: port, Params: params,
		Peers: []awg.Peer{{PublicKey: clientPub, Addr: addr, Email: "u7"}}}); err != nil {
		t.Fatalf("server tunnel: %v", err)
	}
	t.Cleanup(dev.Close)

	// A page on the server's tunnel address: only a working tunnel reaches it.
	ln, err := net.Listen("tcp", net.JoinHostPort(awg.ServerAddr.String(), "8088"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "through the tunnel")
		}))
	}()

	set := &model.Settings{Host: "127.0.0.1", AWGEnabled: true, AWGPort: port, AWGPublicKey: serverPub,
		AWGParams: awg.ToModel(params), AWGDNS: "1.1.1.1"}
	u := model.User{ID: 7, UUID: "11111111-2222-3333-4444-555555555555", WGPrivateKey: clientPriv, AWGSlot: slot}
	servers := []sub.Server{{Set: set, Access: model.UnrestrictedAccess()}}
	ver, _ := exec.Command(bin, "-v").Output()
	ua := "mihomo/" + strings.TrimPrefix(strings.Fields(string(ver) + " x v0")[2], "v")
	if forced := os.Getenv("RP_MIHOMO_UA"); forced != "" {
		ua = forced // hand the proxy to a core the gate would refuse, to see it fail
	}
	profile := sub.ClashYAMLFor(u, servers, ua)
	if !strings.Contains(profile, "amnezia-wg-option") {
		t.Fatalf("no AmneziaWG proxy for %q:\n%s", ua, profile)
	}

	work := t.TempDir()
	mixed := freeTCPPort(t)
	cfg := fmt.Sprintf("mixed-port: %d\nlog-level: warning\n", mixed) + profile
	path := filepath.Join(work, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-d", work, "-f", path)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	proxy, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(mixed))
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 5 * time.Second}
	target := "http://" + net.JoinHostPort(awg.ServerAddr.String(), "8088") + "/"
	var body string
	for i := 0; i < 30; i++ {
		resp, err := c.Get(target)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if body = string(b); body == "through the tunnel" {
				break
			}
		}
		time.Sleep(time.Second)
	}
	if body != "through the tunnel" {
		stats, _ := dev.Stats()
		t.Fatalf("%s: no page through the AmneziaWG tunnel (server peer stats %+v)", ua, stats)
	}
	stats, _ := dev.Stats()
	t.Logf("%s: page fetched through the AmneziaWG 3.1 tunnel; server peer %+v", ua, stats[clientPub])
}

func freeUDPPort(t *testing.T) int {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func freeTCPPort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
