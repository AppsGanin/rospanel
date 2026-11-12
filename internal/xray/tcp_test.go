package xray

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTCPDefaultsCoverTransportsAndEgress(t *testing.T) {
	var inbounds []map[string]any
	for _, network := range []string{"tcp", "raw", "ws", "xhttp", "grpc", "httpupgrade"} {
		inbounds = append(inbounds, map[string]any{"protocol": "vless", "streamSettings": map[string]any{"network": network}})
	}
	for _, protocol := range []string{"vmess", "trojan", "shadowsocks", "http", "socks"} {
		inbounds = append(inbounds, map[string]any{"protocol": protocol})
	}
	data, err := json.Marshal(map[string]any{"inbounds": inbounds, "outbounds": []any{
		map[string]any{"protocol": "freedom"},
		map[string]any{"protocol": "vless", "streamSettings": map[string]any{"network": "ws", "sockopt": map[string]any{"dialerProxy": "warp"}, "extension": 9007199254740993}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := tcpDefaults(data)
	cfg, err := decodeRaw(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"inbounds", "outbounds"} {
		for _, e := range cfg[key].([]any) {
			so := e.(map[string]any)["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
			if so["tcpUserTimeout"] != json.Number("180000") || so["tcpKeepAliveIdle"] != json.Number("15") {
				t.Errorf("%s socket defaults: %v", key, so)
			}
		}
	}
	out := cfg["outbounds"].([]any)[1].(map[string]any)["streamSettings"].(map[string]any)
	if out["extension"] != json.Number("9007199254740993") || out["sockopt"].(map[string]any)["dialerProxy"] != "warp" {
		t.Fatalf("outbound options changed: %v", out)
	}
	if !bytes.Equal(got, tcpDefaults(got)) {
		t.Fatal("reapplying defaults changed config bytes; this would restart live nodes")
	}
}

func TestTCPDefaultsAgainstRealXray(t *testing.T) {
	bin := os.Getenv("RP_XRAY_BIN")
	if bin == "" {
		t.Skip("RP_XRAY_BIN is not set")
	}
	t.Setenv("GODEBUG", "multipathtcp=1")
	dir := t.TempDir()
	s := NewSupervisor(bin, filepath.Join(dir, "config.json"), dir)
	defer s.Stop()
	const uuid = "5783a3e7-e373-51cd-8642-c83782b807c5"
	for protocol, settings := range map[string]any{
		"vless":       map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": uuid}}},
		"vmess":       map[string]any{"clients": []any{map[string]any{"id": uuid}}},
		"trojan":      map[string]any{"clients": []any{map[string]any{"password": "test"}}},
		"shadowsocks": map[string]any{"method": "2022-blake3-aes-128-gcm", "password": "AAAAAAAAAAAAAAAAAAAAAA==", "network": "tcp,udp"},
		"socks":       map[string]any{"auth": "noauth", "udp": true},
		"http":        map[string]any{},
	} {
		t.Run(protocol, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{
				"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": 23456, "protocol": protocol, "settings": settings}},
				"outbounds": []any{map[string]any{"protocol": "freedom"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ValidateBytes(tcpDefaults(data)); err != nil {
				t.Fatal(err)
			}
		})
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	port := freePort(t, "tcp")
	data, err := json.Marshal(map[string]any{
		"log":       map[string]any{"loglevel": "info"},
		"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}}},
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyRaw(data); err != nil {
		t.Fatal(err)
	}
	proxyURL, err := url.Parse("socks5://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := client.Get(target.URL)
		if err == nil {
			body, readErr := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if readErr != nil || string(body) != "ok" {
				t.Fatalf("through Xray: %q, %v", body, readErr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitFor(t, "Xray access log", func() bool {
		return strings.Contains(strings.Join(s.LogTail(), "\n"), "accepted tcp:")
	})
	for _, line := range s.LogTail() {
		if strings.Contains(line, "failed to apply socket options") {
			t.Fatalf("TCP defaults did not reach the socket: %s", line)
		}
	}
}

func TestTCPDefaultsPreserveUDPUnixAndOperatorChoices(t *testing.T) {
	for _, data := range []string{
		`{"inbounds":[{"protocol":"hysteria"},{"protocol":"wireguard"},{"protocol":"vless","listen":"@internal"},{"protocol":"vless","streamSettings":{"network":"kcp"}}],"outbounds":[{"protocol":"wireguard"},{"protocol":"blackhole"}]}`,
		`{"inbounds":[{"protocol":"vless","streamSettings":{"sockopt":{"tcpKeepAliveIdle":0,"tcpKeepAliveInterval":0,"tcpUserTimeout":0,"tcpMptcp":false,"mark":123}}}]}`,
		`not json`,
		`{"outbounds":[{"protocol":"freedom"}]} trailing garbage`,
	} {
		if got := tcpDefaults([]byte(data)); string(got) != data {
			t.Fatalf("unchanged config was rewritten: %s", got)
		}
	}
	data := []byte(`{"inbounds":[{"protocol":"vless","streamSettings":{"sockopt":{"tcpMptcp":true,"tcpKeepAliveIdle":-1}}}]}`)
	cfg, err := decodeRaw(tcpDefaults(data))
	if err != nil {
		t.Fatal(err)
	}
	so := cfg["inbounds"].([]any)[0].(map[string]any)["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
	if _, exists := so["tcpUserTimeout"]; exists {
		t.Fatal("TCP_USER_TIMEOUT was added to explicit MPTCP")
	}
	if so["tcpKeepAliveInterval"] != json.Number("-1") {
		t.Fatalf("keepalive was re-enabled: %v", so)
	}
}
