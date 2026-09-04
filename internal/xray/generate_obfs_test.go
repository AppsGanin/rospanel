package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func mustInbound(t *testing.T, cfg *Config, tag string) *Inbound {
	t.Helper()
	in := findInbound(cfg, tag)
	if in == nil {
		t.Fatalf("inbound %q not generated", tag)
	}
	return in
}

// An install that never set a key must generate exactly the config it did before the
// feature existed: no finalmask block at all, not an empty one. An empty block would
// be a config change on every existing box — an Xray restart nobody asked for.
func TestHysteriaObfsAbsentByDefault(t *testing.T) {
	cfg, err := Generate(baseSettings(), nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	st := mustInbound(t, cfg, TagHysteria).StreamSettings
	if st.FinalMask != nil {
		t.Errorf("finalmask emitted with no obfuscation key set")
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "finalmask") {
		t.Errorf("streamSettings still mentions finalmask: %s", raw)
	}
}

// With a key, the built-in lane carries the Salamander UDP mask. The exact JSON
// matters: Xray picks the mask implementation off the "type" string and reads its own
// settings shape underneath, so a renamed field is a silently unobfuscated lane.
func TestHysteriaObfsBuiltinLane(t *testing.T) {
	set := baseSettings()
	set.HysteriaObfs = "s3cret-key-value"
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	raw, err := json.Marshal(mustInbound(t, cfg, TagHysteria).StreamSettings)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `"finalmask":{"udp":[{"type":"salamander","settings":{"password":"s3cret-key-value"}}]}`
	if !strings.Contains(string(raw), want) {
		t.Errorf("streamSettings = %s\nwant it to contain %s", raw, want)
	}
}

// A custom Hysteria2 inbound carries its own key, and the built-in lane keeps its own
// answer — a shared key would mean one operator setting silently re-keying another lane.
func TestHysteriaObfsCustomInboundIsIndependent(t *testing.T) {
	set := baseSettings()
	in := model.Inbound{
		ID: 7, Enabled: true, Name: "hy-custom", Protocol: model.InbHysteria, Port: 8444,
		Opts: model.InboundOpts{Transport: model.TrHysteria, Security: model.SecTLS, Obfs: "own-key-here"},
	}
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{in}}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if fm := mustInbound(t, cfg, TagHysteria).StreamSettings.FinalMask; fm != nil {
		t.Errorf("the custom inbound's key leaked onto the built-in lane: %+v", fm)
	}
	var custom *Inbound
	for i := range cfg.Inbounds {
		if cfg.Inbounds[i].Port == 8444 {
			custom = &cfg.Inbounds[i]
		}
	}
	if custom == nil {
		t.Fatalf("custom inbound not generated")
	}
	fm := custom.StreamSettings.FinalMask
	if fm == nil || len(fm.UDP) != 1 || fm.UDP[0].Type != "salamander" {
		t.Fatalf("custom inbound finalmask = %+v, want one salamander mask", fm)
	}
	if got, ok := fm.UDP[0].Settings.(SalamanderSettings); !ok || got.Password != "own-key-here" {
		t.Errorf("settings = %+v, want password own-key-here", fm.UDP[0].Settings)
	}
}
