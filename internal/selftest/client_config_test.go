package selftest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func testSettings() *model.Settings {
	return &model.Settings{
		Host:         "vpn.example.com",
		SNI:          "vpn.example.com",
		VLESSPort:    443,
		WSPath:       "/wsxyz",
		HysteriaPort: 60000,
		RealityPort:  8443,
	}
}

func testUser() model.User {
	return model.User{
		UUID:     "11111111-2222-3333-4444-555555555555",
		Password: "s3cr3t-pw",
	}
}

// The client config must be valid JSON with exactly the SOCKS inbound the probe
// dials and a single protocol outbound — a broken config would surface as a false
// "not working" for a server that's actually fine.
func TestBuildVLESSShape(t *testing.T) {
	cfg, err := buildVLESS(testSettings(), testUser(), 10800)
	if err != nil {
		t.Fatalf("buildVLESS: %v", err)
	}
	var w struct {
		Inbounds []struct {
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
		} `json:"inbounds"`
		Outbounds []struct {
			Protocol string `json:"protocol"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(cfg.json, &w); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	if len(w.Inbounds) != 1 || w.Inbounds[0].Protocol != "socks" || w.Inbounds[0].Port != 10800 {
		t.Fatalf("want one socks inbound on 10800, got %+v", w.Inbounds)
	}
	if len(w.Outbounds) != 1 || w.Outbounds[0].Protocol != "vless" {
		t.Fatalf("want one vless outbound, got %+v", w.Outbounds)
	}
	// The Vision flow and the user's UUID must both reach the outbound, or the
	// handshake the test performs isn't the one real clients perform.
	s := string(cfg.json)
	if !strings.Contains(s, visionFlow) {
		t.Errorf("vless outbound missing Vision flow")
	}
	if !strings.Contains(s, testUser().UUID) {
		t.Errorf("vless outbound missing user UUID")
	}
}

// Trojan is reached through the VLESS fallback, so the outbound must dial VLESSPort
// (443), not some separate Trojan port, and carry the WS path + Host header.
func TestBuildTrojanDialsFallbackPort(t *testing.T) {
	set := testSettings()
	cfg, err := buildTrojan(set, testUser(), 10801)
	if err != nil {
		t.Fatalf("buildTrojan: %v", err)
	}
	var w struct {
		Outbounds []struct {
			Settings struct {
				Servers []struct {
					Port     int    `json:"port"`
					Password string `json:"password"`
				} `json:"servers"`
			} `json:"settings"`
			StreamSettings struct {
				WSSettings struct {
					Path    string            `json:"path"`
					Headers map[string]string `json:"headers"`
				} `json:"wsSettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(cfg.json, &w); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	ob := w.Outbounds[0]
	if got := ob.Settings.Servers[0].Port; got != set.VLESSPort {
		t.Errorf("trojan should dial VLESSPort %d, got %d", set.VLESSPort, got)
	}
	if got := ob.StreamSettings.WSSettings.Path; got != set.WSPath {
		t.Errorf("ws path = %q, want %q", got, set.WSPath)
	}
	if got := ob.StreamSettings.WSSettings.Headers["Host"]; got != set.SNI {
		t.Errorf("ws Host header = %q, want %q", got, set.SNI)
	}
}

// Hysteria2 checks version==2 independently in the outbound settings AND in
// hysteriaSettings; a client missing it in either place fails to load with
// "version != 2". This guards the second one, which is easy to forget.
func TestBuildHysteriaHasVersionInBothPlaces(t *testing.T) {
	cfg, err := buildHysteria(testSettings(), testUser(), 10803)
	if err != nil {
		t.Fatalf("buildHysteria: %v", err)
	}
	var w struct {
		Outbounds []struct {
			Protocol string `json:"protocol"`
			Settings struct {
				Version int `json:"version"`
			} `json:"settings"`
			StreamSettings struct {
				HysteriaSettings struct {
					Version int    `json:"version"`
					Auth    string `json:"auth"`
				} `json:"hysteriaSettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(cfg.json, &w); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	ob := w.Outbounds[0]
	if ob.Protocol != "hysteria" {
		t.Errorf("hysteria2 protocol must be %q, got %q", "hysteria", ob.Protocol)
	}
	if ob.Settings.Version != 2 {
		t.Errorf("settings.version = %d, want 2", ob.Settings.Version)
	}
	if ob.StreamSettings.HysteriaSettings.Version != 2 {
		t.Errorf("hysteriaSettings.version = %d, want 2 (Xray checks this independently)",
			ob.StreamSettings.HysteriaSettings.Version)
	}
	if ob.StreamSettings.HysteriaSettings.Auth != testUser().Password {
		t.Errorf("auth must live in hysteriaSettings.auth")
	}
}

// REALITY uses security "reality" (not "tls"), no flow on the gRPC user, and
// carries the donor SNI + public key. A regression here is invisible until a real
// client fails to handshake.
func TestBuildRealityShape(t *testing.T) {
	set := testSettings()
	set.RealityServiceName = "grpcsvc"
	set.RealityPublicKey = "bqO5QKO-QqmWP6llRb9DDsMQJbvW474bCtnYLB9tyno"
	set.RealityDest = "www.cloudflare.com"
	set.RealityShortID = "0123456789abcdef"
	cfg, err := buildReality(set, testUser(), 10804)
	if err != nil {
		t.Fatalf("buildReality: %v", err)
	}
	var w struct {
		Outbounds []struct {
			Settings struct {
				VNext []struct {
					Users []struct {
						Flow string `json:"flow"`
					} `json:"users"`
				} `json:"vnext"`
			} `json:"settings"`
			StreamSettings struct {
				Security        string `json:"security"`
				RealitySettings struct {
					ServerName string `json:"serverName"`
					PublicKey  string `json:"publicKey"`
				} `json:"realitySettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(cfg.json, &w); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	ob := w.Outbounds[0]
	if ob.StreamSettings.Security != "reality" {
		t.Errorf("security = %q, want reality", ob.StreamSettings.Security)
	}
	if f := ob.Settings.VNext[0].Users[0].Flow; f != "" {
		t.Errorf("gRPC REALITY user must have no flow, got %q", f)
	}
	if ob.StreamSettings.RealitySettings.PublicKey != set.RealityPublicKey {
		t.Errorf("realitySettings.publicKey not carried through")
	}
}

func TestRingBufferKeepsTail(t *testing.T) {
	rb := newRingBuffer(8)
	_, _ = rb.Write([]byte("abcdefghij")) // 10 bytes into an 8-byte buffer
	if got := rb.String(); got != "cdefghij" {
		t.Errorf("ring buffer tail = %q, want %q", got, "cdefghij")
	}
}

// A REALITY probe that dies with a bare EOF (donor handshake never completed) must
// get the REALITY-specific hint about the dest, not the generic firewall line —
// that's the difference between an operator fixing the donor and staring at "порт
// закрыт".
func TestExplainFailureRealityHint(t *testing.T) {
	err := context.DeadlineExceeded
	msg := explainFailure(model.ProtoReality, err, "some log with EOF here")
	if !strings.Contains(msg, "REALITY") || !strings.Contains(msg, "донор") {
		t.Errorf("REALITY EOF failure should name the donor/dest, got %q", msg)
	}
	// The same bare timeout on VLESS is a firewall/port problem, not a donor one.
	vmsg := explainFailure(model.ProtoVLESS, err, "")
	if strings.Contains(vmsg, "REALITY") {
		t.Errorf("VLESS failure must not mention REALITY, got %q", vmsg)
	}
}

func TestFirstXrayErrorPicksFailureLine(t *testing.T) {
	log := "2026/07/12 10:00:00 [Info] starting\n" +
		"2026/07/12 10:00:01 [Warning] failed to process outbound traffic: auth failed\n"
	got := firstXrayError(log)
	if !strings.Contains(got, "auth failed") {
		t.Errorf("firstXrayError = %q, want it to mention auth failed", got)
	}
	if strings.Contains(got, "2026/07/12") {
		t.Errorf("firstXrayError should strip the timestamp, got %q", got)
	}
}
