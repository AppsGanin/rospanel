package model

import (
	"encoding/json"
	"sort"
	"strings"
)

// The advanced transport knobs, presented as typed form fields instead of a raw JSON
// textarea, while STILL stored and generated as the single JSON blob Xray reads.
//
// Why keep the blob as the stored/generated form: the config generator and the link
// builder both read exactly that blob, which is what guarantees the inbound and the
// share link can't describe XHTTP differently (the property the whole feature exists
// to hold). The form is a presentation layer over it — assembled on save, taken apart
// on load — so the fields are convenient without a second source of truth to drift.
//
// A per-section "raw" escape hatch carries any key the panel does not surface as a
// field (the exotic structured ones — customSockopt, happyEyeballs, ECH — plus
// whatever a future Xray adds). Surfaced fields win over it on assembly. So nothing
// valid is unreachable and a version bump needs no code change to stay usable.
//
// Numeric shapes matter: a range like maxConcurrency accepts the string "8-32"
// (Xray's Int32Range parses a string), but a plain scalar like scMaxBufferedPosts must
// be a JSON number or Xray rejects it. That is why the range/string fields are strings
// and the scalar fields are *int — the pointer is what lets "unset" (nil, omitempty)
// differ from a real zero.

// XHTTPExtraForm is the field view of xhttpSettings.extra.
type XHTTPExtraForm struct {
	Headers map[string]string `json:"headers,omitempty"`

	XPaddingBytes     string `json:"xPaddingBytes,omitempty"`
	XPaddingObfsMode  bool   `json:"xPaddingObfsMode,omitempty"`
	XPaddingKey       string `json:"xPaddingKey,omitempty"`
	XPaddingHeader    string `json:"xPaddingHeader,omitempty"`
	XPaddingPlacement string `json:"xPaddingPlacement,omitempty"`
	XPaddingMethod    string `json:"xPaddingMethod,omitempty"`

	UplinkHTTPMethod string `json:"uplinkHTTPMethod,omitempty"`

	SessionIDPlacement string `json:"sessionIDPlacement,omitempty"`
	SessionIDKey       string `json:"sessionIDKey,omitempty"`
	SessionIDTable     string `json:"sessionIDTable,omitempty"`
	SessionIDLength    string `json:"sessionIDLength,omitempty"`

	SeqPlacement string `json:"seqPlacement,omitempty"`
	SeqKey       string `json:"seqKey,omitempty"`

	UplinkDataPlacement string `json:"uplinkDataPlacement,omitempty"`
	UplinkDataKey       string `json:"uplinkDataKey,omitempty"`
	UplinkChunkSize     string `json:"uplinkChunkSize,omitempty"`

	NoGRPCHeader bool `json:"noGRPCHeader,omitempty"`
	NoSSEHeader  bool `json:"noSSEHeader,omitempty"`

	ScMaxEachPostBytes   string `json:"scMaxEachPostBytes,omitempty"`
	ScMinPostsIntervalMs string `json:"scMinPostsIntervalMs,omitempty"`
	ScMaxBufferedPosts   *int64 `json:"scMaxBufferedPosts,omitempty"`
	ScStreamUpServerSecs string `json:"scStreamUpServerSecs,omitempty"`
	ServerMaxHeaderBytes *int32 `json:"serverMaxHeaderBytes,omitempty"`

	Xmux *XmuxForm `json:"xmux,omitempty"`

	// Raw is the JSON-object text of everything above that the panel does NOT surface
	// as a field. It crosses the API wire (so the editor can show and resubmit it) but
	// is stripped in assemble before the blob is built — it is a UI container, not an
	// Xray key.
	Raw string `json:"raw,omitempty"`
}

// XmuxForm is the field view of xhttpSettings.extra.xmux.
type XmuxForm struct {
	MaxConcurrency   string `json:"maxConcurrency,omitempty"`
	MaxConnections   string `json:"maxConnections,omitempty"`
	CMaxReuseTimes   string `json:"cMaxReuseTimes,omitempty"`
	HMaxRequestTimes string `json:"hMaxRequestTimes,omitempty"`
	HMaxReusableSecs string `json:"hMaxReusableSecs,omitempty"`
	HKeepAlivePeriod *int64 `json:"hKeepAlivePeriod,omitempty"`
}

func (x XmuxForm) empty() bool {
	return x.MaxConcurrency == "" && x.MaxConnections == "" && x.CMaxReuseTimes == "" &&
		x.HMaxRequestTimes == "" && x.HMaxReusableSecs == "" && x.HKeepAlivePeriod == nil
}

// SockoptForm is the field view of streamSettings.sockopt.
type SockoptForm struct {
	Mark                 *int   `json:"mark,omitempty"`
	TCPFastOpen          *bool  `json:"tcpFastOpen,omitempty"`
	TProxy               string `json:"tproxy,omitempty"`
	DomainStrategy       string `json:"domainStrategy,omitempty"`
	DialerProxy          string `json:"dialerProxy,omitempty"`
	TCPKeepAliveInterval *int   `json:"tcpKeepAliveInterval,omitempty"`
	TCPKeepAliveIdle     *int   `json:"tcpKeepAliveIdle,omitempty"`
	TCPCongestion        string `json:"tcpCongestion,omitempty"`
	TCPWindowClamp       *int   `json:"tcpWindowClamp,omitempty"`
	TCPMaxSeg            *int   `json:"tcpMaxSeg,omitempty"`
	Penetrate            *bool  `json:"penetrate,omitempty"`
	TCPUserTimeout       *int   `json:"tcpUserTimeout,omitempty"`
	V6Only               *bool  `json:"v6only,omitempty"`
	Interface            string `json:"interface,omitempty"`
	TCPMptcp             *bool  `json:"tcpMptcp,omitempty"`
	AddressPortStrategy  string `json:"addressPortStrategy,omitempty"`

	Raw string `json:"raw,omitempty"`
}

// TLSExtraForm is the field view of the extra tlsSettings keys the operator may add.
type TLSExtraForm struct {
	MinVersion              string   `json:"minVersion,omitempty"`
	MaxVersion              string   `json:"maxVersion,omitempty"`
	CipherSuites            string   `json:"cipherSuites,omitempty"`
	RejectUnknownSni        *bool    `json:"rejectUnknownSni,omitempty"`
	CurvePreferences        []string `json:"curvePreferences,omitempty"`
	EnableSessionResumption *bool    `json:"enableSessionResumption,omitempty"`
	DisableSystemRoot       *bool    `json:"disableSystemRoot,omitempty"`
	VerifyPeerCertByName    []string `json:"verifyPeerCertByName,omitempty"`

	Raw string `json:"raw,omitempty"`
}

// surfacedXHTTPKeys / surfacedSockoptKeys / surfacedTLSKeys are the keys each form
// presents as a field. On disassembly they are pulled out of the stored blob into the
// struct; whatever remains goes to the section's Raw escape hatch.
//
// Listed explicitly rather than reflected off the struct tags: an omitempty struct
// marshals to "{}" so there is nothing to read tags off of without reflection, and an
// explicit list states plainly what the panel owns. TestSurfacedKeysMatchForm keeps
// them honest against the structs.
var (
	surfacedXHTTPKeys   = map[string]bool{}
	surfacedSockoptKeys = map[string]bool{}
	surfacedTLSKeys     = map[string]bool{}
)

func init() {
	for _, k := range []string{
		"headers", "xPaddingBytes", "xPaddingObfsMode", "xPaddingKey", "xPaddingHeader",
		"xPaddingPlacement", "xPaddingMethod", "uplinkHTTPMethod", "sessionIDPlacement",
		"sessionIDKey", "sessionIDTable", "sessionIDLength", "seqPlacement", "seqKey",
		"uplinkDataPlacement", "uplinkDataKey", "uplinkChunkSize", "noGRPCHeader",
		"noSSEHeader", "scMaxEachPostBytes", "scMinPostsIntervalMs", "scMaxBufferedPosts",
		"scStreamUpServerSecs", "serverMaxHeaderBytes", "xmux",
	} {
		surfacedXHTTPKeys[k] = true
	}
	for _, k := range []string{
		"mark", "tcpFastOpen", "tproxy", "domainStrategy", "dialerProxy",
		"tcpKeepAliveInterval", "tcpKeepAliveIdle", "tcpCongestion", "tcpWindowClamp",
		"tcpMaxSeg", "penetrate", "tcpUserTimeout", "v6only", "interface", "tcpMptcp",
		"addressPortStrategy",
	} {
		surfacedSockoptKeys[k] = true
	}
	for _, k := range []string{
		"minVersion", "maxVersion", "cipherSuites", "rejectUnknownSni", "curvePreferences",
		"enableSessionResumption", "disableSystemRoot", "verifyPeerCertByName",
	} {
		surfacedTLSKeys[k] = true
	}
}

// assembleBlob merges the marshaled form's non-empty keys over the parsed raw
// fallback and returns the combined JSON object (nil when empty). Surfaced fields win
// on a key clash, so the form is authoritative for anything it presents.
func assembleBlob(form any, raw string) (json.RawMessage, error) {
	base := map[string]json.RawMessage{}
	if t := strings.TrimSpace(raw); t != "" && t != "{}" {
		if err := json.Unmarshal([]byte(t), &base); err != nil {
			return nil, err
		}
	}
	fb, err := json.Marshal(form)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(fb, &fields); err != nil {
		return nil, err
	}
	// "raw" is the form's own escape-hatch container, not an Xray key — it was already
	// parsed into base above. Drop it so it never lands in the blob.
	delete(fields, "raw")
	for k, v := range fields {
		base[k] = v
	}
	if len(base) == 0 {
		return nil, nil
	}
	return json.Marshal(base)
}

// disassembleRaw returns the pretty-printed JSON of every key in the blob that the
// form does not surface — what the section's Raw escape hatch should show.
func disassembleRaw(blob json.RawMessage, surfaced map[string]bool) string {
	if len(blob) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(blob, &m) != nil {
		return ""
	}
	leftover := map[string]json.RawMessage{}
	for k, v := range m {
		if !surfaced[k] {
			leftover[k] = v
		}
	}
	if len(leftover) == 0 {
		return ""
	}
	// Stable key order so the textarea doesn't reshuffle between loads.
	keys := make([]string, 0, len(leftover))
	for k := range leftover {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]json.RawMessage, len(leftover))
	for _, k := range keys {
		ordered[k] = leftover[k]
	}
	b, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// AssembleXHTTPExtra builds the xhttpSettings.extra blob from its form.
func AssembleXHTTPExtra(f XHTTPExtraForm) (json.RawMessage, error) {
	if f.Xmux != nil && f.Xmux.empty() {
		f.Xmux = nil
	}
	return assembleBlob(f, f.Raw)
}

// AssembleSockopt builds the sockopt blob from its form.
func AssembleSockopt(f SockoptForm) (json.RawMessage, error) { return assembleBlob(f, f.Raw) }

// AssembleTLSExtra builds the extra-tlsSettings blob from its form.
func AssembleTLSExtra(f TLSExtraForm) (json.RawMessage, error) { return assembleBlob(f, f.Raw) }

// DisassembleXHTTPExtra fills a form (fields + Raw) from a stored blob.
func DisassembleXHTTPExtra(blob json.RawMessage) XHTTPExtraForm {
	var f XHTTPExtraForm
	if len(blob) > 0 {
		_ = json.Unmarshal(blob, &f)
	}
	f.Raw = disassembleRaw(blob, surfacedXHTTPKeys)
	return f
}

// DisassembleSockopt fills a form from a stored sockopt blob.
func DisassembleSockopt(blob json.RawMessage) SockoptForm {
	var f SockoptForm
	if len(blob) > 0 {
		_ = json.Unmarshal(blob, &f)
	}
	f.Raw = disassembleRaw(blob, surfacedSockoptKeys)
	return f
}

// DisassembleTLSExtra fills a form from a stored TLS-extra blob.
func DisassembleTLSExtra(blob json.RawMessage) TLSExtraForm {
	var f TLSExtraForm
	if len(blob) > 0 {
		_ = json.Unmarshal(blob, &f)
	}
	f.Raw = disassembleRaw(blob, surfacedTLSKeys)
	return f
}

// Enumerations offered in the editor, from Xray's own parser (v26.7.28). Exposed so
// the catalog endpoint hands the UI exactly what Xray accepts — no drift between the
// dropdowns and the validator.
var (
	XHTTPPlacements     = []string{"", "auto", "path", "query", "header", "cookie", "body"}
	XHTTPUplinkMethods  = []string{"", "GET", "POST"}
	SockoptTProxy       = []string{"", "off", "redirect", "tproxy"}
	SockoptDomainStrats = []string{
		"", "asis", "useip", "useipv4", "useipv6", "useipv4v6", "useipv6v4",
		"forceip", "forceipv4", "forceipv6", "forceipv4v6", "forceipv6v4",
	}
	SockoptAddrPortStrats = []string{
		"", "none", "srvportonly", "srvaddressonly", "srvportandaddress",
		"txtportonly", "txtaddressonly", "txtportandaddress",
	}
	TLSVersions = []string{"", "1.0", "1.1", "1.2", "1.3"}
)
