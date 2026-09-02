package xray

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// hysteriaSettings enables both a built-in Hysteria2 lane and the other built-ins,
// so the live-update helpers can be checked for what they include AND exclude.
func hysteriaSettings() *model.Settings {
	s := baseSettings()
	s.RealityEnabled, s.RealityPrivateKey = true, "priv"
	s.RealityPublicKey, s.RealityShortID = "pub", "aabb"
	s.RealityDest = "www.microsoft.com:443"
	return s
}

func customHysteria() model.Inbound {
	return model.Inbound{
		ID: 5, ServerID: model.LocalNodeID, Enabled: true, Name: "quic",
		Protocol: model.InbHysteria, Port: 7443,
		// Hysteria2 runs over QUIC, so it always carries TLS.
		Opts: model.InboundOpts{Transport: "hysteria", Security: "tls"},
	}
}

// Live user ops must never target a Hysteria2 inbound.
//
// Verified against Xray 26.7.28: `adu` answers "unsupported inbound type" and adds
// nobody, while `rmu` prints "Removed 1 user(s)" for a user that never existed — it
// removes nothing and says otherwise. Sending removals there would have the panel
// believe it revoked access it still grants, which is the worst of the three possible
// outcomes.
func TestLiveUserOpsSkipHysteria(t *testing.T) {
	set := hysteriaSettings()
	custom := []model.Inbound{customHysteria()}

	for _, tag := range EnabledInboundTags(set, custom) {
		if tag == TagHysteria || tag == custom[0].Tag() {
			t.Errorf("rmu would target %q — that call reports success without removing anything", tag)
		}
	}

	users := []model.User{{ID: 1, Name: "u", UUID: "11111111-1111-1111-1111-111111111111", Password: "p", Enabled: true}}
	for _, in := range UserInbounds(set, custom, users, model.LocalNodeID, nil) {
		if in.Protocol == "hysteria" {
			t.Errorf("adu would target %q — Xray rejects a QUIC inbound with "+
				"\"unsupported inbound type\" and adds nobody", in.Tag)
		}
	}
}

// The TCP lanes must still be live-updated: that is what makes an add or a revoke
// take effect without touching the running process.
func TestLiveUserOpsStillCoverTheTCPLanes(t *testing.T) {
	set := hysteriaSettings()
	users := []model.User{{ID: 1, Name: "u", UUID: "11111111-1111-1111-1111-111111111111", Password: "p", Enabled: true}}

	tags := map[string]bool{}
	for _, tag := range EnabledInboundTags(set, nil) {
		tags[tag] = true
	}
	for _, want := range []string{TagVLESS, TagReality} {
		if !tags[want] {
			t.Errorf("rmu no longer targets %q — a revoked user would keep that lane", want)
		}
	}

	got := map[string]bool{}
	for _, in := range UserInbounds(set, nil, users, model.LocalNodeID, nil) {
		got[in.Tag] = true
	}
	for _, want := range []string{TagVLESS, TagReality} {
		if !got[want] {
			t.Errorf("adu no longer targets %q — a new user would not reach that lane until a reload", want)
		}
	}
}

// HysteriaInbounds picks what has to be rebuilt, and it must find the operator's own
// QUIC inbounds too — testing only the built-in lane would leave a revoked user
// tunnelling through a custom one.
func TestHysteriaInboundsFindsEveryQUICInbound(t *testing.T) {
	set := hysteriaSettings()
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080", Custom: []model.Inbound{customHysteria()}}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	hy := HysteriaInbounds(cfg)
	if len(hy) != 2 {
		t.Fatalf("found %d hysteria inbounds, want 2 (the built-in lane and the custom one)", len(hy))
	}
	for _, in := range hy {
		if in.Protocol != "hysteria" {
			t.Errorf("%q is not a hysteria inbound", in.Tag)
		}
		// These are handed to `api adi`, which rebuilds the inbound from scratch — so
		// they have to be the WHOLE thing, not the tag-plus-users stub adu takes. An
		// inbound re-added without its port or TLS would come back broken.
		if in.Port == 0 {
			t.Errorf("%q carries no port; re-adding it would fail", in.Tag)
		}
		if in.StreamSettings == nil || in.StreamSettings.TLSSettings == nil {
			t.Errorf("%q carries no TLS settings; the rebuilt lane would reject every client", in.Tag)
		}
	}
}

// A DISABLED built-in lane still has to be rebuilt, which is easy to mistake for a
// bug. Generate always emits the built-in Hysteria inbound and merely empties its
// client list when the protocol is off, so the listener stays up with nobody able to
// authenticate. That empty list is itself a change worth applying — it is how the
// last user loses access — so it must not be filtered out here.
func TestHysteriaInboundsIncludesTheDisabledBuiltinLane(t *testing.T) {
	set := hysteriaSettings()
	set.HysteriaEnabled = false
	cfg, err := Generate(set, nil, Options{PanelDest: "127.0.0.1:8080"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	hy := HysteriaInbounds(cfg)
	if len(hy) != 1 {
		t.Fatalf("found %d hysteria inbounds, want the built-in lane even while disabled", len(hy))
	}
	if s, ok := hy[0].Settings.(HysteriaInboundSettings); ok && len(s.Users) != 0 {
		t.Errorf("the disabled lane still carries %d user(s)", len(s.Users))
	}
}
