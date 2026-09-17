package sub

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The link decodes the way the VK Turn Proxy app decodes it (BackupManager.
// parseConnectionLinkBase64: url-safe base64, a version-1 "connection" object) into a
// server entry that needs nothing typed: the user's key and address, the inbound's
// public key, the relay's address, the call link, and the mode pinned to SRTP-WRAP-S —
// Free Turn Proxy's masked wire — with the masking key and profile.
func TestTurnImportLink(t *testing.T) {
	priv, _, _ := awg.GenerateKey()
	_, serverPub, _ := awg.GenerateKey()
	maskKey := strings.Repeat("0f", 32)
	u := model.User{ID: 4, WGPrivateKey: priv, AWGSlot: 5}
	s := &model.Settings{Host: "vpn.example.com", XrayDNS: "9.9.9.9"}
	in := model.Inbound{ID: 3, Name: "Calls", Protocol: model.InbWireGuard, Port: 56000,
		Opts: model.InboundOpts{WGPublicKey: serverPub, TurnMaskKey: maskKey, TurnLink: "https://vk.com/call/join/abc"}}

	decode := func(l string) (link struct {
		Version  int            `json:"version"`
		Type     string         `json:"type"`
		Settings map[string]any `json:"settings"`
	}) {
		t.Helper()
		data, ok := strings.CutPrefix(l, "vkturnproxy://import?data=")
		if !ok {
			t.Fatalf("link %q", l)
		}
		raw, err := base64.RawURLEncoding.DecodeString(data)
		if err != nil {
			t.Fatalf("base64: %v", err)
		}
		if err := json.Unmarshal(raw, &link); err != nil {
			t.Fatalf("json: %v", err)
		}
		return link
	}

	link := decode(TurnImportLink(u, s, in))
	if link.Version != 1 || link.Type != "connection" {
		t.Errorf("version/type = %d/%q", link.Version, link.Type)
	}
	want := map[string]any{
		"serverName": "Calls", "privateKey": priv, "peerPublicKey": serverPub, "presharedKey": "",
		"tunnelAddress": "10.66.0.5/32", "dnsServers": "9.9.9.9", "vkLink": "https://vk.com/call/join/abc",
		"peerAddress": "vpn.example.com:56000",
		"useDTLS":     true, "useSrtp": false, "useWrap": false, "useWrapA": false, "useWrapS": true,
		"wrapKeyHex": maskKey, "obfProfile": "rtpopus3", "useUDP": false,
	}
	for k, v := range want {
		if link.Settings[k] != v {
			t.Errorf("%s = %v, want %v", k, link.Settings[k], v)
		}
	}
	if len(link.Settings) != len(want) {
		t.Errorf("settings carry %d fields, want %d: %v", len(link.Settings), len(want), link.Settings)
	}

	// No call link: sent empty, and the app keeps the one it has.
	in.Opts.TurnLink = ""
	if got := decode(TurnImportLink(u, s, in)).Settings["vkLink"]; got != "" {
		t.Errorf("vkLink without a call link = %v", got)
	}
	// A user with no tunnel identity yet, or an inbound with no masking key, gets no link
	// rather than a broken one.
	if l := TurnImportLink(model.User{ID: 9}, s, in); l != "" {
		t.Errorf("link without an identity: %q", l)
	}
	in.Opts.TurnMaskKey = ""
	if l := TurnImportLink(u, s, in); l != "" {
		t.Errorf("link without a masking key: %q", l)
	}
}

// The freeturn:// link is Free Turn Proxy's own share format (internal/uri): a version-1
// JSON with the provider, the relay, the mask, and the WireGuard config its Android app
// runs, plus the call link in the field that app reads it from.
func TestFreeTurnImportLink(t *testing.T) {
	priv, _, _ := awg.GenerateKey()
	_, serverPub, _ := awg.GenerateKey()
	maskKey := strings.Repeat("a1", 32)
	u := model.User{ID: 4, WGPrivateKey: priv, AWGSlot: 5}
	s := &model.Settings{Host: "vpn.example.com", XrayDNS: "9.9.9.9"}
	in := model.Inbound{ID: 3, Name: "Calls", Protocol: model.InbWireGuard, Port: 56000,
		Opts: model.InboundOpts{WGPublicKey: serverPub, TurnMaskKey: maskKey, TurnLink: "https://vk.ru/call/join/xyz"}}

	conf := TurnClientConf(u, s, in)
	for _, want := range []string{"PrivateKey = " + priv, "Address = 10.66.0.5/32", "MTU = 1280", "PublicKey = " + serverPub, "Endpoint = 127.0.0.1:9000"} {
		if !strings.Contains(conf, want) {
			t.Errorf("conf lacks %q:\n%s", want, conf)
		}
	}
	l := FreeTurnImportLink(u, s, in, conf)
	data, ok := strings.CutPrefix(l, "freeturn://")
	if !ok {
		t.Fatalf("link %q", l)
	}
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	want := map[string]any{
		"v": float64(1), "provider": "vk", "peer": "vpn.example.com:56000", "obf": "rtpopus3", "key": maskKey,
		"name": "Calls", "vk": "https://vk.ru/call/join/xyz", "wg": conf,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("link carries %d fields, want %d: %v", len(got), len(want), got)
	}
	if l := FreeTurnImportLink(u, s, in, ""); l != "" {
		t.Errorf("link without a config: %q", l)
	}
}

// pbFields reads one protobuf message into field number → raw values: a varint as its
// uvarint encoding, a length-delimited field as its bytes. Enough to check a link
// against wingsv.proto without the generated code.
func pbFields(t *testing.T, b []byte) map[int][][]byte {
	t.Helper()
	out := map[int][][]byte{}
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			t.Fatalf("bad key")
		}
		b = b[n:]
		field := int(key >> 3)
		switch key & 7 {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				t.Fatalf("bad varint in field %d", field)
			}
			out[field] = append(out[field], binary.AppendUvarint(nil, v))
			b = b[n:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || int(l) > len(b[n:]) {
				t.Fatalf("bad length in field %d", field)
			}
			out[field] = append(out[field], b[n:n+int(l)])
			b = b[n+int(l):]
		default:
			t.Fatalf("unexpected wire type %d in field %d", key&7, field)
		}
	}
	return out
}

func pbUint(t *testing.T, f map[int][][]byte, field int) uint64 {
	t.Helper()
	if len(f[field]) != 1 {
		t.Fatalf("field %d present %d times", field, len(f[field]))
	}
	v, _ := binary.Uvarint(f[field][0])
	return v
}

// The WINGS V link decodes the way WingsImportParser does — url-safe base64, a 0x12
// frame byte, zlib, a wingsv.Config — into a VK TURN profile over WireGuard with the
// session mode and WRAP pinned to what the panel's relay speaks.
func TestWingsVImportLink(t *testing.T) {
	priv, _, _ := awg.GenerateKey()
	_, serverPub, _ := awg.GenerateKey()
	u := model.User{ID: 4, WGPrivateKey: priv, AWGSlot: 5}
	s := &model.Settings{Host: "vpn.example.com", XrayDNS: "9.9.9.9, 1.1.1.1"}
	in := model.Inbound{ID: 3, Name: "Calls", Protocol: model.InbWireGuard, Port: 56000,
		Opts: model.InboundOpts{WGPublicKey: serverPub, TurnLink: "https://vk.com/call/join/abc"}}

	l := WingsVImportLink(u, s, in)
	data, ok := strings.CutPrefix(l, "wingsv://")
	if !ok {
		t.Fatalf("link %q", l)
	}
	framed, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil || len(framed) < 2 || framed[0] != 0x12 {
		t.Fatalf("frame: %v %x", err, framed[:min(len(framed), 2)])
	}
	zr, err := zlib.NewReader(bytes.NewReader(framed[1:]))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}

	cfg := pbFields(t, raw)
	if pbUint(t, cfg, 1) != 1 || pbUint(t, cfg, 2) != 10 || pbUint(t, cfg, 5) != 7 {
		t.Errorf("ver/type/backend = %d/%d/%d, want 1/10 (VK_TURN_PROFILE)/7 (VK_TURN)", pbUint(t, cfg, 1), pbUint(t, cfg, 2), pbUint(t, cfg, 5))
	}
	endpoint := func(b []byte) string {
		e := pbFields(t, b)
		return string(e[1][0]) + ":" + strconv.FormatUint(pbUint(t, e, 2), 10)
	}

	turn := pbFields(t, cfg[3][0])
	if got := endpoint(turn[1][0]); got != "vpn.example.com:56000" {
		t.Errorf("turn endpoint = %s", got)
	}
	if string(turn[2][0]) != "https://vk.com/call/join/abc" {
		t.Errorf("turn link = %q", turn[2][0])
	}
	if got := endpoint(turn[6][0]); got != model.TurnClientListen {
		t.Errorf("local endpoint = %s", got)
	}
	if pbUint(t, turn, 9) != 2 || pbUint(t, turn, 18) != 1 || pbUint(t, turn, 19) != 1 {
		t.Errorf("session/tunnel/wrap = %d/%d/%d, want 2 (MAINLINE)/1 (WIREGUARD)/1 (OFF)", pbUint(t, turn, 9), pbUint(t, turn, 18), pbUint(t, turn, 19))
	}
	if string(turn[23][0]) != "Calls" {
		t.Errorf("title = %q", turn[23][0])
	}

	wg := pbFields(t, cfg[4][0])
	iface := pbFields(t, wg[1][0])
	if base64.StdEncoding.EncodeToString(iface[1][0]) != priv {
		t.Error("interface private key is not the user's")
	}
	if string(iface[2][0]) != "10.66.0.5/32" || len(iface[3]) != 2 || string(iface[3][0]) != "9.9.9.9" || pbUint(t, iface, 4) != 1280 {
		t.Errorf("interface = addrs %q dns %q mtu %d", iface[2], iface[3], pbUint(t, iface, 4))
	}
	peer := pbFields(t, wg[2][0])
	if base64.StdEncoding.EncodeToString(peer[1][0]) != serverPub {
		t.Error("peer public key is not the inbound's")
	}
	if got := endpoint(wg[3][0]); got != model.TurnClientListen {
		t.Errorf("wireguard endpoint = %s", got)
	}

	// No call link: the profile still comes, without one — the app keeps the ones it has.
	// No identity, no link.
	in.Opts.TurnLink = ""
	l2 := WingsVImportLink(u, s, in)
	framed2, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(l2, "wingsv://"))
	zr2, err := zlib.NewReader(bytes.NewReader(framed2[1:]))
	if err != nil {
		t.Fatal(err)
	}
	raw2, _ := io.ReadAll(zr2)
	if turn2 := pbFields(t, pbFields(t, raw2)[3][0]); len(turn2[2]) != 0 {
		t.Errorf("a call link went out with none set: %q", turn2[2])
	}
	if l := WingsVImportLink(model.User{ID: 9}, s, in); l != "" {
		t.Errorf("link without an identity: %q", l)
	}
}
