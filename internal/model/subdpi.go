package model

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SubDPI is the client-side DPI evasion the subscription hands out: TLS ClientHello
// fragmentation and noise packets for Xray-core clients (Happ, v2rayNG, v2rayN,
// Streisand — through the Xray JSON subscription format), and record-level
// fragmentation for sing-box. Nothing here changes the server; it only shapes what
// the client is told to do before its first byte reaches a DPI box.
//
// Fragment and noise live on an Xray "freedom" outbound the proxy outbound dials
// through (sockopt.dialerProxy), which is the one arrangement every Xray client
// applies — an app-level toggle exists in some of them, but a setting in the
// subscription reaches every install at once and survives a reinstall.
type SubDPI struct {
	// JSONClients serves the Xray JSON format to Xray-core clients automatically (by
	// User-Agent). Off, the format is still reachable through ?format=xray-json and
	// a response rule; it is the only way fragment/noise reach those apps, so an
	// operator turning them on is told this switch is what delivers them.
	JSONClients bool `json:"json_clients"`

	Fragment         bool   `json:"fragment"`
	FragmentPackets  string `json:"fragment_packets"`  // tlshello | 1-1 | 1-3
	FragmentLength   string `json:"fragment_length"`   // bytes per piece, "min-max"
	FragmentInterval string `json:"fragment_interval"` // ms between pieces, "min-max"

	Noise       bool   `json:"noise"`
	NoiseType   string `json:"noise_type"`   // rand | str | base64
	NoisePacket string `json:"noise_packet"` // rand: length range "min-max"; str/base64: the payload
	NoiseDelay  string `json:"noise_delay"`  // ms before the real packet, "min-max"

	// RecordFragment splits the ClientHello into several TLS records (sing-box
	// tls.record_fragment, ≥1.12) on the sing-box lanes that already carry
	// tls.fragment. Works where the packet-level split is undone by a middlebox.
	RecordFragment bool `json:"record_fragment"`
}

// DefaultSubDPI is everything off, with the values the toggles start from — the
// ones Xray's own documentation uses as its example, which is also what most
// people copy into their apps by hand.
func DefaultSubDPI() SubDPI {
	return SubDPI{
		FragmentPackets: "tlshello", FragmentLength: "100-200", FragmentInterval: "10-20",
		NoiseType: "rand", NoisePacket: "10-20", NoiseDelay: "10-16",
	}
}

// Active reports whether any Xray-side shaping is on.
func (d SubDPI) Active() bool { return d.Fragment || d.Noise }

// Normalized trims every field and fills a blank one with its default, so a form
// that cleared a box saves something Xray accepts rather than an empty string.
func (d SubDPI) Normalized() SubDPI {
	def := DefaultSubDPI()
	or := func(v, dflt string) string {
		if v = strings.TrimSpace(v); v == "" {
			return dflt
		}
		return v
	}
	d.FragmentPackets = or(d.FragmentPackets, def.FragmentPackets)
	d.FragmentLength = or(d.FragmentLength, def.FragmentLength)
	d.FragmentInterval = or(d.FragmentInterval, def.FragmentInterval)
	d.NoiseType = or(d.NoiseType, def.NoiseType)
	d.NoisePacket = or(d.NoisePacket, def.NoisePacket)
	d.NoiseDelay = or(d.NoiseDelay, def.NoiseDelay)
	return d
}

var (
	dpiPackets   = map[string]bool{"tlshello": true, "1-1": true, "1-3": true}
	dpiNoiseType = map[string]bool{"rand": true, "str": true, "base64": true}
	dpiRangeRe   = regexp.MustCompile(`^(\d{1,5})(?:-(\d{1,5}))?$`)
)

// Validate refuses what Xray would refuse — or, worse, silently misread — so a
// typo in a range cannot ship to every client as a config that fails to load.
func (d SubDPI) Validate() error {
	d = d.Normalized()
	if !dpiPackets[d.FragmentPackets] {
		return fieldErr("err.dpiPackets", "fragment: packets — tlshello, 1-1 или 1-3", nil)
	}
	if err := dpiRange("err.dpiFragmentLength", d.FragmentLength, 1, 65535); err != nil {
		return err
	}
	if err := dpiRange("err.dpiFragmentInterval", d.FragmentInterval, 0, 60000); err != nil {
		return err
	}
	if !dpiNoiseType[d.NoiseType] {
		return fieldErr("err.dpiNoiseType", "noise: тип — rand, str или base64", nil)
	}
	switch d.NoiseType {
	case "rand":
		if err := dpiRange("err.dpiNoisePacket", d.NoisePacket, 1, 65535); err != nil {
			return err
		}
	case "str":
		if utf8.RuneCountInString(d.NoisePacket) > 256 {
			return fieldErr("err.dpiNoiseStr", "noise: строка не длиннее 256 символов", nil)
		}
	case "base64":
		if b, err := base64.StdEncoding.DecodeString(d.NoisePacket); err != nil || len(b) == 0 || len(b) > 1024 {
			return fieldErr("err.dpiNoiseBase64", "noise: некорректный base64 (до 1 КБ)", nil)
		}
	}
	return dpiRange("err.dpiNoiseDelay", d.NoiseDelay, 0, 60000)
}

// dpiRange accepts "n" or "min-max" within [lo, hi] with min ≤ max.
func dpiRange(code, v string, lo, hi int) error {
	m := dpiRangeRe.FindStringSubmatch(strings.TrimSpace(v))
	bad := func() error {
		return fieldErr(code, "диапазон «{{value}}»: число или min-max в пределах {{lo}}–{{hi}}",
			map[string]any{"value": v, "lo": lo, "hi": hi})
	}
	if m == nil {
		return bad()
	}
	a, _ := strconv.Atoi(m[1])
	b := a
	if m[2] != "" {
		b, _ = strconv.Atoi(m[2])
	}
	if a < lo || b > hi || a > b {
		return bad()
	}
	return nil
}

// xrayCoreClients are the User-Agent fragments of the apps that run Xray-core and
// take a JSON config from a subscription. Lower-case; matched as substrings.
var xrayCoreClients = []string{"happ", "v2rayng", "v2rayn", "streisand"}

// IsXrayCoreClient reports whether a User-Agent belongs to an app the Xray JSON
// format is meant for.
func IsXrayCoreClient(ua string) bool {
	ua = strings.ToLower(ua)
	for _, k := range xrayCoreClients {
		if strings.Contains(ua, k) {
			return true
		}
	}
	return false
}
