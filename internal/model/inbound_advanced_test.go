package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func i64(v int64) *int64 { return &v }
func b(v bool) *bool     { return &v }

// A *bool field must round-trip: rejectUnknownSni set to true survives.
func TestTLSExtraBoolRoundTrip(t *testing.T) {
	in := TLSExtraForm{MinVersion: "1.2", MaxVersion: "1.3", RejectUnknownSni: b(true)}
	blob, err := AssembleTLSExtra(in)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	out := DisassembleTLSExtra(blob)
	if out.RejectUnknownSni == nil || !*out.RejectUnknownSni {
		t.Errorf("rejectUnknownSni did not round-trip: %+v", out)
	}
	if out.MinVersion != "1.2" || out.MaxVersion != "1.3" {
		t.Errorf("versions did not round-trip: %+v", out)
	}
}

// A populated form must survive assemble → blob → disassemble unchanged. This is what
// lets the editor show fields, store the blob the generator/link read, and re-open the
// same fields — without the two representations drifting.
func TestXHTTPExtraRoundTrip(t *testing.T) {
	in := XHTTPExtraForm{
		Headers:              map[string]string{"X-Env": "prod"},
		XPaddingBytes:        "100-1000",
		XPaddingObfsMode:     true,
		NoSSEHeader:          true,
		UplinkHTTPMethod:     "GET",
		SessionIDPlacement:   "query",
		SessionIDKey:         "auth",
		SeqPlacement:         "query",
		SeqKey:               "id",
		SessionIDTable:       "alphabet",
		SessionIDLength:      "20",
		ScStreamUpServerSecs: "20-80",
		ScMaxBufferedPosts:   i64(30),
		Xmux: &XmuxForm{
			MaxConcurrency: "8-32", CMaxReuseTimes: "36-96", HKeepAlivePeriod: i64(0),
		},
	}
	blob, err := AssembleXHTTPExtra(in)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	// Every surfaced key that was set must be a key Xray actually reads, so the
	// assembled blob always passes the whitelist validation.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(blob, &m); err != nil {
		t.Fatalf("blob not an object: %v", err)
	}
	for k := range m {
		if !XHTTPExtraKeys[k] {
			t.Errorf("assembled a key Xray does not know: %q", k)
		}
	}

	out := DisassembleXHTTPExtra(blob)
	out.Raw = "" // no unsurfaced keys here; compare the fields
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip changed the form:\n in=%+v\nout=%+v", in, out)
	}
}

// A valid-but-unsurfaced key (customSockopt is exotic and has no field) must survive
// through the Raw escape hatch, and a surfaced field must win over the same key given
// in Raw — otherwise a future Xray addition would be silently lost on the next edit.
func TestSockoptRawFallback(t *testing.T) {
	f := DisassembleSockopt(json.RawMessage(`{"tcpCongestion":"bbr","customSockopt":[{"level":"6"}]}`))
	if f.TCPCongestion != "bbr" {
		t.Errorf("surfaced field not read: %+v", f)
	}
	if !jsonHasKey(f.Raw, "customSockopt") {
		t.Errorf("unsurfaced key did not land in Raw: %q", f.Raw)
	}

	// Surfaced field wins over the same key set in Raw.
	f2 := SockoptForm{TCPCongestion: "cubic", Raw: `{"tcpCongestion":"bbr","mark":7}`}
	blob, err := AssembleSockopt(f2)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(blob, &m)
	if m["tcpCongestion"] != "cubic" {
		t.Errorf("surfaced field should win over Raw, got %v", m["tcpCongestion"])
	}
	if m["mark"] == nil {
		t.Errorf("a Raw-only key should be preserved: %v", m)
	}
}

// Empty everywhere ⇒ nil blob, so the generated config carries no inert block.
func TestAssembleEmptyIsNil(t *testing.T) {
	if blob, _ := AssembleXHTTPExtra(XHTTPExtraForm{}); blob != nil {
		t.Errorf("empty form should assemble to nil, got %s", blob)
	}
	if blob, _ := AssembleSockopt(SockoptForm{}); blob != nil {
		t.Errorf("empty sockopt should assemble to nil, got %s", blob)
	}
	// An xmux with only an all-empty sub-form must not emit an empty "xmux":{} block.
	if blob, _ := AssembleXHTTPExtra(XHTTPExtraForm{Xmux: &XmuxForm{}}); blob != nil {
		t.Errorf("empty xmux should not be emitted, got %s", blob)
	}
}

// Every surfaced key must be one Xray actually reads (a key in the validation
// whitelist), so assembling a form can never produce a blob the whitelist rejects.
func TestSurfacedKeysAreWhitelisted(t *testing.T) {
	for k := range surfacedXHTTPKeys {
		if !XHTTPExtraKeys[k] {
			t.Errorf("XHTTP surfaces %q but the whitelist does not allow it", k)
		}
	}
	for k := range surfacedSockoptKeys {
		if !SockoptKeys[k] {
			t.Errorf("sockopt surfaces %q but the whitelist does not allow it", k)
		}
	}
	for k := range surfacedTLSKeys {
		if !TLSExtraKeys[k] {
			t.Errorf("TLS surfaces %q but the whitelist does not allow it", k)
		}
	}
}

// The surfaced-key lists must match the form structs' json tags exactly, or a field
// added to a struct would silently leak into Raw on load (or an entry removed from a
// struct would strand a key nowhere).
func TestSurfacedKeysMatchForms(t *testing.T) {
	check := func(name string, v any, surfaced map[string]bool) {
		tags := structTags(v)
		for tag := range tags {
			if !surfaced[tag] {
				t.Errorf("%s: field %q is not in the surfaced set", name, tag)
			}
		}
		for k := range surfaced {
			if !tags[k] {
				t.Errorf("%s: surfaced key %q has no struct field", name, k)
			}
		}
	}
	check("XHTTP", XHTTPExtraForm{}, surfacedXHTTPKeys)
	check("sockopt", SockoptForm{}, surfacedSockoptKeys)
	check("TLS", TLSExtraForm{}, surfacedTLSKeys)
}

// structTags reflects a struct's top-level json tag names, ignoring "-".
func structTags(v any) map[string]bool {
	out := map[string]bool{}
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" {
			continue
		}
		name := tag
		if c := indexByte(tag, ','); c >= 0 {
			name = tag[:c]
		}
		if name == "-" || name == "" || name == "raw" {
			continue // "raw" is the escape-hatch container, not a surfaced Xray key
		}
		out[name] = true
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func jsonHasKey(text, key string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

func TestSockoptTrustedXForwardedFor(t *testing.T) {
	rawJSON := `{"trustedXForwardedFor":["173.245.48.0/20","104.16.0.0/13"],"acceptProxyProtocol":true}`
	f := DisassembleSockopt(json.RawMessage(rawJSON))
	if !jsonHasKey(f.Raw, "trustedXForwardedFor") {
		t.Errorf("trustedXForwardedFor should stay in raw fallback: %s", f.Raw)
	}
	if !jsonHasKey(f.Raw, "acceptProxyProtocol") {
		t.Errorf("acceptProxyProtocol should stay in raw fallback: %s", f.Raw)
	}
	blob, err := AssembleSockopt(f)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if err := validateJSONObject(blob, SockoptKeys, "sockopt"); err != nil {
		t.Errorf("validateJSONObject rejected trustedXForwardedFor/acceptProxyProtocol: %v", err)
	}
}
