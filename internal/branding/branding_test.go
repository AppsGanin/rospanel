package branding

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fieldCode returns the translation code of a validation error, so a test can
// assert WHICH rule fired rather than match Russian prose.
func fieldCode(t *testing.T, err error) string {
	t.Helper()
	var fe *model.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error %v is not a *model.FieldError; the panel could not translate it", err)
	}
	return fe.Code
}

func TestNameFallsBackToTheDefault(t *testing.T) {
	if got := Name(""); got != DefaultName {
		t.Errorf("Name(\"\") = %q", got)
	}
	if got := Name("   "); got != DefaultName {
		t.Errorf("a whitespace name was kept: %q", got)
	}
	if got := Name("  Acme  "); got != "Acme" {
		t.Errorf("Name = %q, want it trimmed", got)
	}
}

// Whatever is stored, ParseTheme must hand back a complete palette: a missing,
// malformed or invalid field is the default, never an empty CSS value.
func TestParseThemeAlwaysYieldsACompletePalette(t *testing.T) {
	def := DefaultTheme()
	if got := ParseTheme(""); got != def {
		t.Errorf("empty JSON = %+v, want defaults", got)
	}
	if got := ParseTheme("{not json"); got != def {
		t.Errorf("malformed JSON = %+v, want defaults", got)
	}
	got := ParseTheme(`{"accent":" #FF0000 ","text":"red","bg":"#12345"}`)
	if got.Accent != "#ff0000" {
		t.Errorf("accent = %q, want it trimmed and lowercased", got.Accent)
	}
	if got.Text != def.Text || got.Bg != def.Bg {
		t.Errorf("invalid colours were not replaced by defaults: %+v", got)
	}
	if got.Muted != def.Muted || got.Surface != def.Surface {
		t.Errorf("absent colours were not defaulted: %+v", got)
	}
}

func TestNormalizeTheme(t *testing.T) {
	// Nothing set persists as nothing, so the settings row does not carry a JSON
	// blob of empty strings that reads as "customised".
	if got, err := NormalizeTheme(Theme{}); err != nil || got != "" {
		t.Errorf("NormalizeTheme(empty) = %q, %v; want \"\"", got, err)
	}
	if got, err := NormalizeTheme(Theme{Accent: "  "}); err != nil || got != "" {
		t.Errorf("a whitespace-only theme should store nothing, got %q, %v", got, err)
	}

	stored, err := NormalizeTheme(Theme{Accent: " #ABCDEF ", Bg: "#000000"})
	if err != nil {
		t.Fatal(err)
	}
	back := ParseTheme(stored)
	if back.Accent != "#abcdef" || back.Bg != "#000000" || back.Text != DefaultTheme().Text {
		t.Errorf("round trip = %+v", back)
	}

	// Each colour has its own code, because the field name is a translated word.
	for _, tc := range []struct {
		theme Theme
		code  string
	}{
		{Theme{Accent: "blue"}, "err.badColorAccent"},
		{Theme{Text: "#12345"}, "err.badColorText"},
		{Theme{Muted: "#gggggg"}, "err.badColorMuted"},
		{Theme{Bg: "123456"}, "err.badColorBg"},
		{Theme{Surface: "#1234567"}, "err.badColorSurface"},
	} {
		_, err := NormalizeTheme(tc.theme)
		if err == nil {
			t.Errorf("%+v was accepted", tc.theme)
			continue
		}
		if got := fieldCode(t, err); got != tc.code {
			t.Errorf("%+v: code %q, want %q", tc.theme, got, tc.code)
		}
	}
}

func TestColourMaths(t *testing.T) {
	if got := Darken("#ffffff", 0.5); got != "#7f7f7f" {
		t.Errorf("Darken = %s", got)
	}
	if got := Lighten("#000000", 0.5); got != "#7f7f7f" {
		t.Errorf("Lighten = %s", got)
	}
	if got := Darken("#0d4cd3", 0); got != "#0d4cd3" {
		t.Errorf("Darken by 0 changed the colour: %s", got)
	}
	// Garbage passes through rather than becoming "#000000": the CSS then shows
	// the bad value instead of silently painting everything black.
	if got := Darken("red", 0.2); got != "red" {
		t.Errorf("Darken(invalid) = %s", got)
	}
	if got := Lighten("", 0.2); got != "" {
		t.Errorf("Lighten(\"\") = %q", got)
	}
	// Status text is darkened on a light surface and lightened on a dark one,
	// so it stays readable whichever theme the operator picked.
	if got := Fg("#059669", "#ffffff"); got != Darken("#059669", 0.12) {
		t.Errorf("Fg on white = %s", got)
	}
	if got := Fg("#059669", "#111111"); got != Lighten("#059669", 0.4) {
		t.Errorf("Fg on near-black = %s", got)
	}
	// An unparseable surface counts as light (luminance 1), the safe default for
	// the stock white cards.
	if got := Fg("#059669", "nope"); got != Darken("#059669", 0.12) {
		t.Errorf("Fg on invalid surface = %s", got)
	}
}

func TestLogoContentTypeFromMagic(t *testing.T) {
	if got := LogoContentType(pngBytes(t, 1, 1)); got != "image/png" {
		t.Errorf("png = %s", got)
	}
	if got := LogoContentType(jpegBytes(t)); got != "image/jpeg" {
		t.Errorf("jpeg = %s", got)
	}
	if got := LogoContentType(DefaultLogo()); got != "image/svg+xml" {
		t.Errorf("default logo = %s", got)
	}
	if got := LogoContentType(nil); got != "image/svg+xml" {
		t.Errorf("empty = %s", got)
	}
}

func TestLogoLifecycle(t *testing.T) {
	dir := t.TempDir()
	if HasCustomLogo(dir) {
		t.Fatal("a fresh data dir reports a custom logo")
	}
	got, err := ReadLogo(dir)
	if err != nil || !bytes.Equal(got, DefaultLogo()) {
		t.Fatalf("ReadLogo on a fresh dir = %d bytes, %v; want the built-in SVG", len(got), err)
	}
	if !strings.Contains(string(DefaultLogo()), "<svg") {
		t.Error("the embedded default logo is not an SVG")
	}

	logo := pngBytes(t, 64, 64)
	if err := SaveLogo(dir, bytes.NewReader(logo)); err != nil {
		t.Fatalf("SaveLogo(png): %v", err)
	}
	if !HasCustomLogo(dir) {
		t.Error("HasCustomLogo is false after a save")
	}
	if got, _ := ReadLogo(dir); !bytes.Equal(got, logo) {
		t.Error("ReadLogo did not return the uploaded bytes")
	}

	// A second upload replaces the first; JPEG is accepted alongside PNG.
	jpg := jpegBytes(t)
	if err := SaveLogo(dir, bytes.NewReader(jpg)); err != nil {
		t.Fatalf("SaveLogo(jpeg): %v", err)
	}
	if got, _ := ReadLogo(dir); !bytes.Equal(got, jpg) || LogoContentType(got) != "image/jpeg" {
		t.Error("the JPEG upload did not replace the PNG")
	}

	if err := DeleteLogo(dir); err != nil {
		t.Fatalf("DeleteLogo: %v", err)
	}
	if HasCustomLogo(dir) {
		t.Error("the logo survived DeleteLogo")
	}
	if got, _ := ReadLogo(dir); !bytes.Equal(got, DefaultLogo()) {
		t.Error("the default did not come back after reset")
	}
	// Resetting twice is not an error: the button may be pressed on a stock panel.
	if err := DeleteLogo(dir); err != nil {
		t.Errorf("DeleteLogo on a stock panel: %v", err)
	}
}

// Every rejection must name its rule (a code the panel translates) and must
// leave no file behind, or a half-validated upload would become the logo.
func TestSaveLogoRejections(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		code string
	}{
		{"empty", nil, "err.emptyFile"},
		{"over the byte cap", bytes.Repeat([]byte{0x89}, MaxLogoBytes+1), "err.logoTooBig"},
		{"svg", DefaultLogo(), "err.needPngJpeg"},
		{"gif", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), "err.needPngJpeg"},
		{"truncated png", pngBytes(t, 4, 4)[:10], "err.needPngJpeg"},
		{"too wide", pngBytes(t, maxLogoDim+1, 1), "err.imageTooLarge"},
		{"too tall", pngBytes(t, 1, maxLogoDim+1), "err.imageTooLarge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			err := SaveLogo(dir, bytes.NewReader(tc.body))
			if err == nil {
				t.Fatal("accepted")
			}
			if got := fieldCode(t, err); got != tc.code {
				t.Errorf("code %q, want %q", got, tc.code)
			}
			if HasCustomLogo(dir) {
				t.Error("a rejected upload was written to disk")
			}
		})
	}
	// The cap is inclusive: exactly MaxLogoBytes is judged on content, not size.
	// (These bytes are not an image, so the next rule fires — the point is which.)
	err := SaveLogo(t.TempDir(), bytes.NewReader(bytes.Repeat([]byte{0x89}, MaxLogoBytes)))
	if got := fieldCode(t, err); got != "err.needPngJpeg" {
		t.Errorf("exactly MaxLogoBytes: code %q, want the size rule not to fire", got)
	}
	// An image right at the dimension limit is fine.
	if err := SaveLogo(t.TempDir(), bytes.NewReader(pngBytes(t, maxLogoDim, 1))); err != nil {
		t.Errorf("a %dpx-wide image was rejected: %v", maxLogoDim, err)
	}
}
