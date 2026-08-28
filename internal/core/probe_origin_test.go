package core

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// The digest line has to survive every combination of "we know where this is from",
// because the geo tables are optional and either one can be missing on its own.
func TestProbeOriginDegradesToNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		hit  model.ProbeHit
		want string
	}{
		{"country and operator", model.ProbeHit{Country: "NL", Org: "Hetzner"}, " · 🇳🇱 NL · Hetzner"},
		{"country only", model.ProbeHit{Country: "US"}, " · 🇺🇸 US"},
		{"operator only", model.ProbeHit{Org: "Some ISP"}, " · Some ISP"},
		{"neither", model.ProbeHit{}, ""},
		// A code the flag table cannot render must still name the country rather than
		// dropping it: the letters are the useful part, the glyph is decoration.
		{"unrenderable code", model.ProbeHit{Country: "X1"}, " · X1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeOrigin(tc.hit); got != tc.want {
				t.Errorf("probeOrigin = %q, want %q", got, tc.want)
			}
		})
	}
}

// The digest is sent with HTML parse mode. The operator name comes from our own ASN
// table rather than from anything a scanner sent, but it is one table refresh away
// from carrying whatever a third party put in it, and an unescaped "<" there breaks
// the message for every admin at best.
func TestProbeOriginEscapesHTML(t *testing.T) {
	got := probeOrigin(model.ProbeHit{Country: "NL", Org: `Evil <b>x</b> & "co"`})
	if strings.Contains(got, "<b>") {
		t.Errorf("operator name reaches the message unescaped: %q", got)
	}
	if !strings.Contains(got, "&lt;b&gt;") {
		t.Errorf("expected the markup escaped, got %q", got)
	}
}

// Registry names run long enough to swamp a ten-line digest, and cutting them by
// bytes would hand Telegram a half-character.
func TestProbeOriginShortensLongOperatorNames(t *testing.T) {
	long := "MAYTINHVPSTTT-VN VPSTTT COMPUTER COMPANY LIMITED AND SUBSIDIARIES"
	got := probeOrigin(model.ProbeHit{Country: "VN", Org: long})
	if strings.Contains(got, "SUBSIDIARIES") {
		t.Errorf("long operator name was not shortened: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("shortened name should say so, got %q", got)
	}
	// Cyrillic is where a byte-wise cut shows up as broken UTF-8 rather than a short
	// string, so the count has to be in runes.
	cyr := strings.Repeat("Ростелеком ", 10)
	if got := probeOrigin(model.ProbeHit{Org: cyr}); !utf8.ValidString(got) {
		t.Errorf("shortening produced invalid UTF-8: %q", got)
	}
}
