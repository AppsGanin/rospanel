package branding

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestName(t *testing.T) {
	if got := Name(""); got != DefaultName {
		t.Errorf("Name(\"\") = %q; want %q", got, DefaultName)
	}
	if got := Name("   "); got != DefaultName {
		t.Errorf("Name(\"   \") = %q; want %q", got, DefaultName)
	}
	if got := Name("MyVPN"); got != "MyVPN" {
		t.Errorf("Name(\"MyVPN\") = %q; want %q", got, "MyVPN")
	}
}

func TestThemeParsingAndNormalization(t *testing.T) {
	def := DefaultTheme()
	if def.Accent != "#0d4cd3" {
		t.Errorf("DefaultTheme.Accent = %q; want #0d4cd3", def.Accent)
	}

	// Empty JSON should yield default theme
	parsed := ParseTheme("")
	if parsed != def {
		t.Errorf("ParseTheme(\"\") = %+v; want %+v", parsed, def)
	}

	// Partial valid JSON
	customJSON := `{"accent": "#ff0000", "text": "#111111"}`
	customParsed := ParseTheme(customJSON)
	if customParsed.Accent != "#ff0000" || customParsed.Text != "#111111" {
		t.Errorf("ParseTheme partial failed: %+v", customParsed)
	}
	if customParsed.Surface != def.Surface {
		t.Errorf("ParseTheme surface = %q; want %q (default fallback)", customParsed.Surface, def.Surface)
	}

	// Invalid hex fields should fallback
	invalidJSON := `{"accent": "invalid", "text": "#12345"}`
	invalidParsed := ParseTheme(invalidJSON)
	if invalidParsed.Accent != def.Accent || invalidParsed.Text != def.Text {
		t.Errorf("ParseTheme invalid hex did not fallback to default: %+v", invalidParsed)
	}

	// NormalizeTheme valid
	normJSON, err := NormalizeTheme(Theme{Accent: "#00FF00", Text: "#000000"})
	if err != nil {
		t.Fatalf("NormalizeTheme valid failed: %v", err)
	}
	if !strings.Contains(normJSON, "#00ff00") {
		t.Errorf("NormalizeTheme lowercasing failed: %s", normJSON)
	}

	// NormalizeTheme invalid color
	_, err = NormalizeTheme(Theme{Accent: "badcolor"})
	if err == nil {
		t.Error("NormalizeTheme(\"badcolor\") expected error; got nil")
	}

	// NormalizeTheme empty theme
	emptyNorm, err := NormalizeTheme(Theme{})
	if err != nil || emptyNorm != "" {
		t.Errorf("NormalizeTheme(empty) = (%q, %v); want (\"\", nil)", emptyNorm, err)
	}
}

func TestColorMathAndLuminance(t *testing.T) {
	// Darken
	dark := Darken("#ffffff", 0.5)
	if dark != "#7f7f7f" {
		t.Errorf("Darken(#ffffff, 0.5) = %q; want #7f7f7f", dark)
	}
	// Invalid darken input returns as-is
	if Darken("invalid", 0.5) != "invalid" {
		t.Errorf("Darken(invalid) did not return input unchanged")
	}

	// Lighten
	light := Lighten("#000000", 0.5)
	if light != "#7f7f7f" {
		t.Errorf("Lighten(#000000, 0.5) = %q; want #7f7f7f", light)
	}

	// Fg contrast calculation
	fgDarkSurface := Fg("#0d4cd3", "#000000")  // dark surface -> lightened
	fgLightSurface := Fg("#0d4cd3", "#ffffff") // light surface -> slightly darkened
	if fgDarkSurface == fgLightSurface {
		t.Errorf("Fg did not differentiate dark vs light surface: %q vs %q", fgDarkSurface, fgLightSurface)
	}
}

func TestLogoContentType(t *testing.T) {
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	if ct := LogoContentType(pngHeader); ct != "image/png" {
		t.Errorf("LogoContentType(png) = %q; want image/png", ct)
	}

	jpegHeader := []byte{0xff, 0xd8, 0xff, 0xe0}
	if ct := LogoContentType(jpegHeader); ct != "image/jpeg" {
		t.Errorf("LogoContentType(jpeg) = %q; want image/jpeg", ct)
	}

	svgHeader := []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>")
	if ct := LogoContentType(svgHeader); ct != "image/svg+xml" {
		t.Errorf("LogoContentType(svg) = %q; want image/svg+xml", ct)
	}
}

func TestSaveAndManageLogo(t *testing.T) {
	dir := t.TempDir()

	if HasCustomLogo(dir) {
		t.Error("HasCustomLogo on empty dir = true; want false")
	}

	// Default logo read
	defaultLogo, err := ReadLogo(dir)
	if err != nil || len(defaultLogo) == 0 {
		t.Fatalf("ReadLogo default failed: %v", err)
	}

	// Create a valid 10x10 PNG
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode failed: %v", err)
	}

	// Save valid logo
	if err := SaveLogo(dir, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("SaveLogo valid failed: %v", err)
	}

	if !HasCustomLogo(dir) {
		t.Error("HasCustomLogo after save = false; want true")
	}

	customLogo, err := ReadLogo(dir)
	if err != nil || !bytes.Equal(customLogo, buf.Bytes()) {
		t.Errorf("ReadLogo after save mismatch: len %d vs %d", len(customLogo), buf.Len())
	}

	// Save empty file error
	if err := SaveLogo(dir, bytes.NewReader(nil)); err == nil {
		t.Error("SaveLogo empty file expected error; got nil")
	}

	// Save non-image error
	if err := SaveLogo(dir, bytes.NewReader([]byte("not an image at all"))); err == nil {
		t.Error("SaveLogo non-image expected error; got nil")
	}

	// Save oversized image dimension error (>1024x1024)
	bigImg := image.NewRGBA(image.Rect(0, 0, 1025, 10))
	var bigBuf bytes.Buffer
	_ = png.Encode(&bigBuf, bigImg)
	if err := SaveLogo(dir, bytes.NewReader(bigBuf.Bytes())); err == nil {
		t.Error("SaveLogo oversized dimensions expected error; got nil")
	}

	// Delete logo
	if err := DeleteLogo(dir); err != nil {
		t.Fatalf("DeleteLogo failed: %v", err)
	}
	if HasCustomLogo(dir) {
		t.Error("HasCustomLogo after DeleteLogo = true; want false")
	}
	// Delete again should be idempotent (no error on not exist)
	if err := DeleteLogo(dir); err != nil {
		t.Errorf("DeleteLogo idempotent failed: %v", err)
	}
}
