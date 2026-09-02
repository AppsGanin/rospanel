package extsub

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const (
	vlessLink   = "vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?type=tcp&security=reality&sni=max.ru&fp=chrome&pbk=PUBKEY&sid=ab12&flow=xtls-rprx-vision#Amsterdam%201"
	trojanLink  = "trojan://secret@example.com:443?security=tls&sni=example.com&type=ws&path=%2Ftr&host=example.com#Trojan%20WS"
	hy2Link     = "hysteria2://pw%40x@5.6.7.8:443?sni=5.6.7.8&mport=20000-30000#Hy2"
	ssModern    = "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@9.9.9.9:8388#SS%20new"
	ssLegacyRaw = "aes-256-gcm:password@9.9.9.9:8388"
)

func vmessLinkFor(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(b)
}

func TestDecodeAcceptsPlainBase64AndComments(t *testing.T) {
	plain := "# header\n" + vlessLink + "\n\n" + trojanLink + "\n"
	if got := Decode([]byte(plain)); len(got) != 2 || got[0] != vlessLink {
		t.Fatalf("plain: %v", got)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(plain))
	if got := Decode([]byte(b64 + "\n")); len(got) != 2 || got[1] != trojanLink {
		t.Fatalf("base64: %v", got)
	}
	// URL-safe base64 with no padding, as some panels write it.
	raw := base64.RawURLEncoding.EncodeToString([]byte(hy2Link))
	if got := Decode([]byte(raw)); len(got) != 1 || got[0] != hy2Link {
		t.Fatalf("raw url base64: %v", got)
	}
	// A body that is valid base64 but decodes to no links stays what it is.
	if got := Decode([]byte("vless")); len(got) != 1 || got[0] != "vless" {
		t.Fatalf("false base64: %v", got)
	}
}

func TestParseEveryScheme(t *testing.T) {
	cases := []struct {
		link        string
		proto, host string
		port        int
		name        string
	}{
		{vlessLink, "vless", "1.2.3.4", 443, "Amsterdam 1"},
		{trojanLink, "trojan", "example.com", 443, "Trojan WS"},
		{hy2Link, "hysteria2", "5.6.7.8", 443, "Hy2"},
		{"hy2://pw@[2001:db8::1]:8443#v6", "hysteria2", "2001:db8::1", 8443, "v6"},
		{ssModern, "ss", "9.9.9.9", 8388, "SS new"},
		{"ss://" + base64.StdEncoding.EncodeToString([]byte(ssLegacyRaw)) + "#SS%20old", "ss", "9.9.9.9", 8388, "SS old"},
		{vmessLinkFor(t, map[string]any{"add": "7.7.7.7", "port": "8080", "id": "uuid-1", "ps": "VM", "net": "ws", "path": "/w", "host": "h", "tls": "tls"}), "vmess", "7.7.7.7", 8080, "VM"},
	}
	for _, c := range cases {
		ep, ok := Parse(c.link)
		if !ok {
			t.Errorf("%s: not parsed", c.link)
			continue
		}
		if ep.Protocol != c.proto || ep.Host != c.host || ep.Port != c.port || ep.Name != c.name {
			t.Errorf("%s: got %+v", c.link, ep)
		}
	}
	for _, bad := range []string{"", "http://example.com", "vless://@host:443", "vless://id@host:99999", "socks5://1.2.3.4:1080", "vmess://not-base64!"} {
		if _, ok := Parse(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestLegacyShadowsocksIsRespelled(t *testing.T) {
	old := "ss://" + base64.StdEncoding.EncodeToString([]byte(ssLegacyRaw)) + "#old"
	ep, _ := Parse(old)
	if !strings.HasPrefix(ep.Link, "ss://") || !strings.Contains(ep.Link, "@9.9.9.9:8388") {
		t.Fatalf("legacy link not re-spelled: %s", ep.Link)
	}
	modern, _ := Parse(ssModern)
	if ep.Key() != modern.Key() {
		t.Fatal("the same server in both spellings must share a key")
	}
}

func TestKeyIgnoresTheLabelButNotTheCredential(t *testing.T) {
	a, _ := Parse(vlessLink)
	b, _ := Parse(strings.Replace(vlessLink, "#Amsterdam%201", "#Renamed", 1))
	c, _ := Parse(strings.Replace(vlessLink, "11111111", "99999999", 1))
	if a.Key() != b.Key() {
		t.Fatal("a renamed server must keep its key")
	}
	if a.Key() == c.Key() {
		t.Fatal("a different credential is a different server")
	}
	if got := ParseAll([]string{vlessLink, vlessLink, trojanLink}); len(got) != 2 {
		t.Fatalf("duplicates kept: %d", len(got))
	}
}

func TestXrayOutbound(t *testing.T) {
	o, ok := XrayOutbound(vlessLink, "t1")
	if !ok || o["tag"] != "t1" || o["protocol"] != "vless" {
		t.Fatalf("vless: %v %v", ok, o)
	}
	ss := o["streamSettings"].(map[string]any)
	if ss["security"] != "reality" || ss["realitySettings"].(map[string]any)["publicKey"] != "PUBKEY" {
		t.Fatalf("reality settings lost: %v", ss)
	}
	o, ok = XrayOutbound(hy2Link, "t2")
	if !ok || o["protocol"] != "hysteria" {
		t.Fatalf("hysteria2: %v %v", ok, o)
	}
	hs := o["streamSettings"].(map[string]any)
	if hs["hysteriaSettings"].(map[string]any)["auth"] != "pw@x" {
		t.Fatalf("hysteria password not unescaped: %v", hs)
	}
	if hs["finalmask"] == nil {
		t.Fatal("mport did not become a udpHop range")
	}
	o, ok = XrayOutbound(ssModern, "t3")
	if !ok || o["protocol"] != "shadowsocks" {
		t.Fatalf("ss: %v %v", ok, o)
	}
	srv := o["settings"].(map[string]any)["servers"].([]map[string]any)[0]
	if srv["method"] != "aes-256-gcm" || srv["password"] != "password" {
		t.Fatalf("ss credentials: %v", srv)
	}
	vm := vmessLinkFor(t, map[string]any{"add": "7.7.7.7", "port": 8080, "id": "uuid-1", "aid": "0", "net": "ws", "path": "/w", "host": "h.example", "tls": "tls", "sni": "h.example"})
	o, ok = XrayOutbound(vm, "t4")
	if !ok || o["protocol"] != "vmess" {
		t.Fatalf("vmess: %v %v", ok, o)
	}
	vs := o["streamSettings"].(map[string]any)
	if vs["network"] != "ws" || vs["security"] != "tls" || vs["wsSettings"].(map[string]any)["path"] != "/w" {
		t.Fatalf("vmess stream: %v", vs)
	}
	if _, ok := XrayOutbound("socks5://1.2.3.4:1080", "x"); ok {
		t.Fatal("a proxy scheme is not a share link")
	}
	// The outbound must survive JSON: it is what ends up in a config file.
	if _, err := json.Marshal(o); err != nil {
		t.Fatal(err)
	}
}

func TestClashAndSingBoxDropWhatTheyCannotSay(t *testing.T) {
	ep, _ := Parse(vlessLink)
	if _, line, ok := ClashProxy(ep); !ok || !strings.Contains(line, "reality-opts") || !strings.Contains(line, "flow: xtls-rprx-vision") {
		t.Fatalf("clash reality: %v %s", ok, line)
	}
	if o, ok := SingBoxOutbound(ep, "sb"); !ok || o["tls"].(map[string]any)["reality"] == nil {
		t.Fatalf("sing-box reality: %v %v", ok, o)
	}
	xhttp, _ := Parse("vless://id@1.2.3.4:443?type=xhttp&security=tls&sni=a.b#x")
	if _, _, ok := ClashProxy(xhttp); ok {
		t.Fatal("mihomo has no XHTTP; the entry must be dropped, not approximated")
	}
	if _, ok := SingBoxOutbound(xhttp, "x"); ok {
		t.Fatal("sing-box has no XHTTP either")
	}
	plainTrojan, _ := Parse("trojan://pw@1.2.3.4:443?security=none#t")
	if _, _, ok := ClashProxy(plainTrojan); ok {
		t.Fatal("a trojan without TLS is not a mihomo trojan")
	}
	hy, _ := Parse(hy2Link)
	if _, line, ok := ClashProxy(hy); !ok || !strings.Contains(line, `ports: "20000-30000"`) {
		t.Fatalf("clash hysteria2 ports: %v %s", ok, line)
	}
	if o, ok := SingBoxOutbound(hy, "h"); !ok || o["server_ports"].([]string)[0] != "20000:30000" {
		t.Fatalf("sing-box hysteria2 ports: %v %v", ok, o)
	}
	ssEp, _ := Parse(ssModern)
	if _, line, ok := ClashProxy(ssEp); !ok || !strings.Contains(line, `cipher: "aes-256-gcm"`) {
		t.Fatalf("clash ss: %v %s", ok, line)
	}
}

func TestValidateSource(t *testing.T) {
	if err := ValidateSource(" "); err == nil {
		t.Error("empty accepted")
	}
	if err := ValidateSource("just some text"); err == nil {
		t.Error("text with no links accepted")
	}
	if err := ValidateSource(vlessLink + "\n" + trojanLink); err != nil {
		t.Errorf("inline links refused: %v", err)
	}
	if err := ValidateSource("http://127.0.0.1/sub"); err == nil {
		t.Error("a loopback URL must be refused by the SSRF gate")
	}
	if IsURL(vlessLink) || !IsURL("https://example.com/sub") {
		t.Error("IsURL")
	}
}

func TestHappByteSwapsAreInvolutions(t *testing.T) {
	in := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}
	if got := swapAdjacent(swapAdjacent(in)); string(got) != string(in) {
		t.Fatalf("swapAdjacent twice: %v", got)
	}
	if got := swapBlockHalves(swapBlockHalves(in)); string(got) != string(in) {
		t.Fatalf("swapBlockHalves twice: %v", got)
	}
	if got := swapBlockHalves([]byte{1, 2, 3, 4}); got[0] != 3 || got[2] != 1 {
		t.Fatalf("ABCD→CDAB: %v", got)
	}
	// Every vendored RSA key must parse: a typo in the base64 would only surface on
	// the first happ:// link an operator pastes.
	for i := range happRSAKeys {
		if _, err := happRSAKey(i); err != nil {
			t.Errorf("key %d: %v", i+1, err)
		}
	}
	if _, err := DecryptHapp("happ://crypt/not-a-valid-block"); err == nil {
		t.Error("garbage decrypted")
	}
}
