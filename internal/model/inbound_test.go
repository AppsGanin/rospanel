package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func vlessWS(id int64, name string, port int) Inbound {
	in := Inbound{
		ID: id, Enabled: true, Name: name, Protocol: InbVLESS, Port: port,
		Opts: InboundOpts{Transport: TrWS, Security: SecTLS, Path: "/p"},
	}
	in.Normalize()
	return in
}

func hy2(id int64, name string, port, hopStart, hopEnd int) Inbound {
	in := Inbound{
		ID: id, Enabled: true, Name: name, Protocol: InbHysteria, Port: port,
		Opts: InboundOpts{HopStart: hopStart, HopEnd: hopEnd},
	}
	in.Normalize()
	return in
}

// Normalize owns the fields the operator must not set by hand: Vision is a raw-TCP
// flow and is silently wrong anywhere else, and Hysteria2 has no transport, security
// or fingerprint to choose.
func TestNormalizeOwnsDerivedFields(t *testing.T) {
	in := Inbound{
		Name: "x", Protocol: InbVLESS, Port: 8443,
		Opts: InboundOpts{Transport: TrWS, Security: SecTLS, Path: "p", Flow: VisionFlowName},
	}
	in.Normalize()
	if in.Opts.Flow != "" {
		t.Errorf("Vision must be cleared off a non-TCP transport, got %q", in.Opts.Flow)
	}
	if in.Opts.Path != "/p" {
		t.Errorf("path should get exactly one leading slash, got %q", in.Opts.Path)
	}

	tcp := Inbound{
		Name: "x", Protocol: InbVLESS, Port: 8443,
		Opts: InboundOpts{Transport: TrTCP, Security: SecTLS},
	}
	tcp.Normalize()
	if tcp.Opts.Flow != VisionFlowName {
		t.Errorf("raw TCP VLESS should get Vision, got %q", tcp.Opts.Flow)
	}

	h := Inbound{
		Name: "h", Protocol: InbHysteria, Port: 60000,
		Opts: InboundOpts{Transport: TrGRPC, Security: SecReality, FP: "chrome", HopEnd: 60100},
	}
	h.Normalize()
	if h.Opts.Transport != TrHysteria || h.Opts.Security != SecTLS {
		t.Errorf("hysteria transport/security not normalized: %+v", h.Opts)
	}
	if h.Opts.FP != "" || h.Opts.RealityDest != "" {
		t.Errorf("hysteria must carry no uTLS/REALITY fields: %+v", h.Opts)
	}
	if h.Opts.HopInterval != "5-10" {
		t.Errorf("hop interval should default when a range is set, got %q", h.Opts.HopInterval)
	}
}

// Combinations the panel can't emit a working client config for are rejected at the
// door rather than reaching Xray.
func TestValidateRejectsBadCombos(t *testing.T) {
	cases := []struct {
		name string
		in   Inbound
	}{
		{"REALITY on Trojan", Inbound{
			Name: "t", Protocol: InbTrojan, Port: 8443,
			Opts: InboundOpts{Transport: TrTCP, Security: SecReality, RealityDest: "www.apple.com"},
		}},
		{"plaintext on raw TCP", Inbound{
			Name: "v", Protocol: InbVLESS, Port: 8443,
			Opts: InboundOpts{Transport: TrTCP, Security: SecNone},
		}},
		{"ws without a path", Inbound{
			Name: "v", Protocol: InbVLESS, Port: 8443,
			Opts: InboundOpts{Transport: TrWS, Security: SecTLS},
		}},
		{"grpc without a service name", Inbound{
			Name: "v", Protocol: InbVLESS, Port: 8443,
			Opts: InboundOpts{Transport: TrGRPC, Security: SecTLS},
		}},
		{"REALITY without a donor", Inbound{
			Name: "v", Protocol: InbVLESS, Port: 8443,
			Opts: InboundOpts{Transport: TrXHTTP, Security: SecReality, Path: "/p"},
		}},
		{"REALITY donor that isn't a domain", Inbound{
			Name: "v", Protocol: InbVLESS, Port: 8443,
			Opts: InboundOpts{Transport: TrXHTTP, Security: SecReality, Path: "/p", RealityDest: "1.2.3.4"},
		}},
		{"port out of range", Inbound{
			Name: "v", Protocol: InbVLESS, Port: 70000,
			Opts: InboundOpts{Transport: TrWS, Security: SecTLS, Path: "/p"},
		}},
		{"name that would break a Clash document", Inbound{
			Name: `bad"quote`, Protocol: InbVLESS, Port: 8443,
			Opts: InboundOpts{Transport: TrWS, Security: SecTLS, Path: "/p"},
		}},
	}
	for _, c := range cases {
		in := c.in
		in.Normalize()
		if err := in.Validate(); err == nil {
			t.Errorf("%s: expected rejection", c.name)
		}
	}
}

// The cross-inbound rules: two listeners can't share a port, a display name can't
// repeat (it becomes a colliding sing-box tag), and nothing may sit on a built-in
// lane's port — the collision would otherwise surface as an Xray that won't start.
func TestValidateInboundSetCollisions(t *testing.T) {
	reserved := ReservedPorts{443: "VLESS-Vision :443", 8443: "VLESS-XHTTP-REALITY"}

	if err := ValidateInboundSet([]Inbound{
		vlessWS(1, "A", 9001), vlessWS(2, "B", 9001),
	}, reserved, nil); err == nil {
		t.Error("expected a duplicate-port rejection")
	}
	if err := ValidateInboundSet([]Inbound{
		vlessWS(1, "A", 9001), vlessWS(2, "a", 9002),
	}, reserved, nil); err == nil {
		t.Error("expected a duplicate-name rejection (names are case-insensitive)")
	}
	if err := ValidateInboundSet([]Inbound{vlessWS(1, "A", 443)}, reserved, nil); err == nil {
		t.Error("expected a reserved-port rejection")
	}
	if err := ValidateInboundSet([]Inbound{
		vlessWS(1, "A", 9001), vlessWS(2, "B", 9002),
	}, reserved, nil); err != nil {
		t.Errorf("a valid pair was rejected: %v", err)
	}

	// A disabled inbound occupies nothing, so parking a spare on a busy port is fine.
	off := vlessWS(2, "B", 9001)
	off.Enabled = false
	if err := ValidateInboundSet([]Inbound{vlessWS(1, "A", 9001), off}, reserved, nil); err != nil {
		t.Errorf("a disabled inbound must not collide: %v", err)
	}
}

// A custom inbound must not be able to take a built-in lane's display name: both
// become node names in the same Clash/sing-box document, and a client that sees a
// duplicate tag rejects the WHOLE profile — costing the user every other server too.
func TestValidateInboundSetRejectsBuiltinName(t *testing.T) {
	taken := []string{ProtoVLESS, ProtoReality, ProtoHysteria}
	if err := ValidateInboundSet([]Inbound{vlessWS(1, ProtoVLESS, 9001)}, nil, taken); err == nil {
		t.Error("expected a built-in lane name to be refused")
	}
	// Case-insensitively, since that is how the collision is judged at render time.
	if err := ValidateInboundSet([]Inbound{vlessWS(1, "vless-tcp-tls", 9001)}, nil, taken); err == nil {
		t.Error("expected the built-in name check to be case-insensitive")
	}
	if err := ValidateInboundSet([]Inbound{vlessWS(1, "Резерв", 9001)}, nil, taken); err != nil {
		t.Errorf("a distinct name was refused: %v", err)
	}
}

// Two nftables funnels over the same UDP range would fight, and a funnel that
// swallows another inbound's base port silently steals its traffic. Both are caught
// before anything is written.
func TestValidateInboundSetHopRanges(t *testing.T) {
	// H1 funnels 5001–5100 onto :5000, H2 funnels 4001–5050 onto :4000. They share
	// 5001–5050, so one of the two would never see that traffic.
	if err := ValidateInboundSet([]Inbound{
		hy2(1, "H1", 5000, 5001, 5100),
		hy2(2, "H2", 4000, 4001, 5050),
	}, nil, nil); err == nil {
		t.Error("expected overlapping hop ranges to be rejected")
	}
	if err := ValidateInboundSet([]Inbound{
		hy2(1, "H1", 5000, 5001, 5100),
		vlessWS(2, "W", 5050),
	}, nil, nil); err == nil {
		t.Error("expected a hop range swallowing another inbound's port to be rejected")
	}
	if err := ValidateInboundSet([]Inbound{
		hy2(1, "H1", 5000, 5001, 5100),
	}, ReservedPorts{5050: "HYSTERIA-UDP"}, nil); err == nil {
		t.Error("expected a hop range covering a reserved port to be rejected")
	}
	if err := ValidateInboundSet([]Inbound{
		hy2(1, "H1", 5000, 5001, 5100),
		hy2(2, "H2", 6000, 6001, 6100),
	}, nil, nil); err != nil {
		t.Errorf("two disjoint hop ranges were rejected: %v", err)
	}
}

// The capability matrix is what stops a broken proxy entry from reaching a client
// that would then reject the whole profile. XHTTP is the load-bearing case: sing-box
// has no such transport at all, while mihomo has it for VLESS only.
func TestSubscriptionFormatSupport(t *testing.T) {
	if SupportsSingBox(InbVLESS, TrXHTTP) {
		t.Error("sing-box has no XHTTP transport")
	}
	if !SupportsClash(InbVLESS, TrXHTTP) {
		t.Error("mihomo supports xhttp for VLESS")
	}
	if SupportsClash(InbTrojan, TrXHTTP) {
		t.Error("mihomo's xhttp is VLESS-only")
	}
	if !SupportsSingBox(InbTrojan, TrHTTPUpgrade) {
		t.Error("sing-box supports httpupgrade")
	}
	if SupportsClash(InbVLESS, TrHTTPUpgrade) {
		t.Error("mihomo reaches httpupgrade only through a ws option, not as its own transport")
	}
	if !SupportsClash(InbHysteria, TrHysteria) || !SupportsSingBox(InbHysteria, TrHysteria) {
		t.Error("hysteria2 is supported by both structured formats")
	}
}

// The advanced blobs are the one place an operator can put arbitrary text into the
// generated config, so the door has to be narrow: a JSON object, and only keys Xray
// actually reads. An unknown key is the dangerous case — Xray drops it silently, so
// the setting looks applied and isn't.
func TestAdvancedBlobValidation(t *testing.T) {
	withExtra := func(raw string) Inbound {
		in := Inbound{
			Enabled: true, Name: "X", Protocol: InbVLESS, Port: 9443,
			Opts: InboundOpts{
				Transport: TrXHTTP, Security: SecTLS, Path: "/p",
				XHTTPExtra: json.RawMessage(raw),
			},
		}
		in.Normalize()
		return in
	}

	ok := withExtra(`{"xmux":{"maxConcurrency":"8-32"},"xPaddingBytes":"100-1000"}`)
	if err := ok.Validate(); err != nil {
		t.Errorf("a valid extra block was rejected: %v", err)
	}

	// The reference config that prompted this feature carries two keys Xray's parser
	// has no field for — exactly the failure mode this check exists for.
	bad := withExtra(`{"sessionPlacement":"query","sessionKey":"auth"}`)
	err := bad.Validate()
	if err == nil {
		t.Fatal("expected unknown extra keys to be refused")
	}
	if !strings.Contains(err.Error(), "sessionKey") || !strings.Contains(err.Error(), "sessionPlacement") {
		t.Errorf("the error should name the offending keys, got: %v", err)
	}

	// host/path/mode are set through their own fields; Xray overwrites whatever the
	// blob says, so accepting them here would only look like it worked.
	pathInExtra := withExtra(`{"path":"/other"}`)
	if err := pathInExtra.Validate(); err == nil {
		t.Error("expected path inside extra to be refused")
	}
	notObject := withExtra(`["not","an","object"]`)
	if err := notObject.Validate(); err == nil {
		t.Error("expected a non-object blob to be refused")
	}

	// An empty object is not a setting — it should normalize away entirely rather
	// than emit an inert block into the config.
	if empty := withExtra(`{}`); empty.Opts.XHTTPExtra != nil {
		t.Errorf("an empty object should normalize to nothing, got %s", empty.Opts.XHTTPExtra)
	}

	// TLS keys the panel owns must not be overridable: serverName and alpn are
	// mirrored into the link, so an override here would desync every client.
	tls := Inbound{
		Enabled: true, Name: "T", Protocol: InbVLESS, Port: 9444,
		Opts: InboundOpts{
			Transport: TrWS, Security: SecTLS, Path: "/p",
			TLSExtra: json.RawMessage(`{"alpn":["h2"]}`),
		},
	}
	tls.Normalize()
	if err := tls.Validate(); err == nil {
		t.Error("expected alpn to be refused in the TLS extra block")
	}
}

// A transport change must not leave another transport's advanced settings behind,
// or they would reappear in the generated config after an unrelated edit.
func TestNormalizeClearsForeignAdvancedFields(t *testing.T) {
	in := Inbound{
		Name: "X", Protocol: InbVLESS, Port: 9443,
		Opts: InboundOpts{
			Transport: TrWS, Security: SecTLS, Path: "/p",
			XHTTPExtra: json.RawMessage(`{"noSSEHeader":true}`),
			HeaderType: "http", HeaderHosts: []string{"a.example.com"},
			Authority: "x.example.com", MultiMode: true,
		},
	}
	in.Normalize()
	if in.Opts.XHTTPExtra != nil {
		t.Error("XHTTP extra should be dropped on a non-XHTTP transport")
	}
	if in.Opts.HeaderType != "" || in.Opts.HeaderHosts != nil {
		t.Error("the TCP masquerade should be dropped on a non-TCP transport")
	}
	if in.Opts.Authority != "" || in.Opts.MultiMode {
		t.Error("gRPC fields should be dropped on a non-gRPC transport")
	}
}

// A flag is how an operator labels a location, so the name charset has to carry one.
//
// It is not one character: a flag is a PAIR of regional indicator symbols, and the
// rest of the emoji machinery — skin tones, ZWJ sequences, variation selectors —
// lives in categories that letters-and-digits never reached. Pasting one used to
// fail the save with "invalid name", which is a strange thing for a panel to have
// an opinion about.
func TestLaneNamesCarryEmoji(t *testing.T) {
	for _, name := range []string{
		"🇳🇱 Нидерланды",   // the reported case: flag + Cyrillic
		"🇩🇪",               // a flag on its own
		"🇷🇺 РФ 🚀",         // several, mixed with text
		"VLESS-TCP-TLS",     // the default, still fine
		"Node ①",            // a number form, category No
		"👋🏽 hi",            // skin-tone modifier (category Sk)
		"👨‍👩‍👧 family",        // a ZWJ sequence
		"⚠️ backup",         // variation selector
	} {
		in := Inbound{Name: name, Protocol: InbVLESS, Port: 8443,
			Opts: InboundOpts{Transport: TrTCP, Security: SecTLS}}
		in.Normalize()
		if err := in.Validate(); err != nil {
			t.Errorf("name %q rejected: %v", name, err)
		}
	}
	// The point of the allowlist is unchanged: what breaks a sing-box tag or a Clash
	// node name stays out.
	for _, name := range []string{`say "hi"`, "a:b", "{tpl}", "a,b", "back\\slash", "new\nline"} {
		in := Inbound{Name: name, Protocol: InbVLESS, Port: 8443,
			Opts: InboundOpts{Transport: TrTCP, Security: SecTLS}}
		in.Normalize()
		if err := in.Validate(); err == nil {
			t.Errorf("name %q was accepted and would break a generated config", name)
		}
	}
}
