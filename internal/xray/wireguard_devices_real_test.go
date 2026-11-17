package xray

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
)

// TestWireGuardDeviceKeysAgainstRealXray: a user with a main key and device keys has
// a peer per key on a plain WireGuard inbound, as Generate builds it. Both devices get
// through at once; a device key dropped live stops that device and leaves the main
// one going; a device key added live gets through; Xray is never restarted. Set
// RP_XRAY_BIN to an Xray binary to run it.
func TestWireGuardDeviceKeysAgainstRealXray(t *testing.T) {
	bin := os.Getenv("RP_XRAY_BIN")
	if bin == "" {
		t.Skip("RP_XRAY_BIN is not set")
	}
	dir := t.TempDir()
	apiPort, wgPort := freePort(t, "tcp"), freePort(t, "udp")
	apiAddr := fmt.Sprintf("127.0.0.1:%d", apiPort)
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

	key := func() string {
		priv, _, err := awg.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		return priv
	}
	serverPriv, serverPub, _ := awg.GenerateKey()
	in := model.Inbound{ID: 31, Enabled: true, Name: "WG", Protocol: model.InbWireGuard, Port: wgPort,
		Opts: model.InboundOpts{WGPrivateKey: serverPriv, WGPublicKey: serverPub}}
	in.Normalize()
	mainKey, phoneKey, laptopKey := key(), key(), key()
	user := func(devices ...model.AWGKey) []model.User {
		return []model.User{{ID: 1, UUID: "u-1", WGPrivateKey: mainKey, AWGSlot: awg.FirstSlot, AWGDevices: devices}}
	}
	phone := model.AWGKey{ID: 1, Key: phoneKey, Slot: awg.FirstSlot + 1}
	laptop := model.AWGKey{ID: 2, Key: laptopKey, Slot: awg.FirstSlot + 2}
	wgInbound := func(users []model.User) Inbound {
		gen, err := Generate(baseSettings(), users, Options{PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{in}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := findInbound(gen, in.Tag())
		if got == nil {
			t.Fatal("no wireguard inbound")
		}
		return *got
	}
	first := wgInbound(user(phone))
	if n := len(first.Settings.(WireGuardInboundSettings).Peers); n != 2 {
		t.Fatalf("%d peers, want the main key and the phone's", n)
	}
	cfg := map[string]any{
		"log":   map[string]any{"loglevel": "warning"},
		"api":   map[string]any{"tag": "api", "services": []string{"HandlerService", "StatsService"}},
		"stats": map[string]any{},
		"inbounds": []any{
			map[string]any{"tag": "api", "listen": "127.0.0.1", "port": apiPort, "protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1"}},
			first,
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{
				"finalRules": []any{map[string]any{"action": "allow", "ip": []string{targetIP + "/32"}}}}},
		},
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api"},
		}},
	}
	data, _ := json.Marshal(cfg)
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
	endpoint := fmt.Sprintf("127.0.0.1:%d", wgPort)
	clients := map[string]*http.Client{
		"main":   wireGuardClient(t, bin, dir, "main", mainKey, awg.FirstSlot, serverPub, endpoint),
		"phone":  wireGuardClient(t, bin, dir, "phone", phoneKey, phone.Slot, serverPub, endpoint),
		"laptop": wireGuardClient(t, bin, dir, "laptop", laptopKey, laptop.Slot, serverPub, endpoint),
	}
	get := func(who string) bool {
		resp, err := clients[who].Get(targetURL)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b) == "ok"
	}
	waitLong(t, "the main key and the phone to get through together", func() bool { return get("main") && get("phone") })
	if get("laptop") {
		t.Fatal("a key the user does not hold got through")
	}

	// The phone's key goes, the laptop's comes: live.
	if err := sup.SyncWireGuard(apiAddr, []Inbound{wgInbound(user(laptop))}); err != nil {
		t.Fatal(err)
	}
	waitLong(t, "the laptop to get through once added", func() bool { return get("laptop") })
	waitLong(t, "the main key to keep working", func() bool { return get("main") })
	if get("phone") {
		t.Error("the phone still gets through after its key was deleted")
	}
	if pid := runningProc(sup).cmd.Process.Pid; pid != pidBefore {
		t.Errorf("xray was restarted (pid %d → %d)", pidBefore, pid)
	}
}
