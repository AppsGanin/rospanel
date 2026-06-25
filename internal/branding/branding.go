// Package branding holds the configurable panel identity: display name, accent
// colour and logo. The name and accent live in the settings table (so they ride
// along in the SQLite backup); a custom logo is a file under <dataDir>/branding/
// (which the data-dir tar backup already captures). When nothing is configured
// the built-in РосПанель defaults apply.
package branding

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

//go:embed default-logo.svg
var defaultLogoSVG []byte

// DefaultLogo returns the built-in РосПанель logo (SVG).
func DefaultLogo() []byte { return defaultLogoSVG }

const (
	// DefaultName is shown when no custom panel name is set.
	DefaultName = "РосПанель"

	MaxNameLen   = 48
	MaxLogoBytes = 512 << 10 // 512 KiB
	maxLogoDim   = 1024
)

// Theme is the configurable colour palette. Every field is a #rrggbb hex; an
// empty field falls back to the matching DefaultTheme() value.
type Theme struct {
	Accent  string `json:"accent"`  // primary colour (drives the whole brand-* ramp)
	Text    string `json:"text"`    // main heading/body text
	Muted   string `json:"muted"`   // secondary/muted text
	Bg      string `json:"bg"`      // page background base
	Surface string `json:"surface"` // cards / inputs / panels
}

// DefaultTheme is the stock РосПанель palette (Госуслуги-style blue on a soft
// blue page with white surfaces).
func DefaultTheme() Theme {
	return Theme{
		Accent:  "#0d4cd3",
		Text:    "#0a1b2e",
		Muted:   "#5b6b7e",
		Bg:      "#eaf1fb",
		Surface: "#ffffff",
	}
}

var accentRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Name returns the configured panel name or the default.
func Name(panelName string) string {
	if n := strings.TrimSpace(panelName); n != "" {
		return n
	}
	return DefaultName
}

// ParseTheme decodes the stored theme JSON and fills any empty/invalid field
// from DefaultTheme(), so the result is always a complete, valid palette.
func ParseTheme(themeJSON string) Theme {
	def := DefaultTheme()
	out := def
	if strings.TrimSpace(themeJSON) != "" {
		var t Theme
		if json.Unmarshal([]byte(themeJSON), &t) == nil {
			out = Theme{
				Accent:  pick(t.Accent, def.Accent),
				Text:    pick(t.Text, def.Text),
				Muted:   pick(t.Muted, def.Muted),
				Bg:      pick(t.Bg, def.Bg),
				Surface: pick(t.Surface, def.Surface),
			}
		}
	}
	return out
}

func pick(v, fallback string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if accentRe.MatchString(v) {
		return v
	}
	return fallback
}

// NormalizeTheme validates each provided colour and returns the JSON to persist.
// Empty fields are dropped (⇒ default applies); a non-empty non-hex field errors.
func NormalizeTheme(t Theme) (string, error) {
	clean := func(name, v string) (string, error) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || accentRe.MatchString(v) {
			return v, nil
		}
		return "", fmt.Errorf("цвет «%s» должен быть в формате #RRGGBB", name)
	}
	var err error
	if t.Accent, err = clean("акцент", t.Accent); err != nil {
		return "", err
	}
	if t.Text, err = clean("текст", t.Text); err != nil {
		return "", err
	}
	if t.Muted, err = clean("приглушённый текст", t.Muted); err != nil {
		return "", err
	}
	if t.Bg, err = clean("фон", t.Bg); err != nil {
		return "", err
	}
	if t.Surface, err = clean("поверхность", t.Surface); err != nil {
		return "", err
	}
	if t == (Theme{}) {
		return "", nil // nothing set ⇒ store empty, all defaults apply
	}
	b, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Darken mixes hex toward black by frac (0..1); used to derive the darker accent
// shade for hover/active states. Invalid input is returned unchanged.
func Darken(hex string, frac float64) string {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return hex
	}
	m := func(c int) int { return int(float64(c) * (1 - frac)) }
	return fmt.Sprintf("#%02x%02x%02x", m(r), m(g), m(b))
}

// Lighten mixes hex toward white by frac (0..1). Invalid input is returned as-is.
func Lighten(hex string, frac float64) string {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return hex
	}
	m := func(c int) int { return c + int(float64(255-c)*frac) }
	return fmt.Sprintf("#%02x%02x%02x", m(r), m(g), m(b))
}

// luminance is the sRGB relative luminance (0..1) of a hex colour.
func luminance(hex string) float64 {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return 1
	}
	f := func(c int) float64 {
		s := float64(c) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*f(r) + 0.7152*f(g) + 0.0722*f(b)
}

// Fg returns base (an accent/status colour) adjusted for contrast against the
// given surface: lightened on dark surfaces, slightly darkened on light ones.
// This is the colour to use for that hue's TEXT so it stays readable in any theme.
func Fg(base, surface string) string {
	if luminance(surface) < 0.4 {
		return Lighten(base, 0.4)
	}
	return Darken(base, 0.12)
}

func parseHex(hex string) (r, g, b int, ok bool) {
	hex = strings.TrimSpace(hex)
	if !accentRe.MatchString(hex) {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseInt(hex[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff, true
}

func brandingDir(dataDir string) string { return filepath.Join(dataDir, "branding") }
func logoPath(dataDir string) string    { return filepath.Join(brandingDir(dataDir), "logo") }

// HasCustomLogo reports whether a custom logo file exists on disk.
func HasCustomLogo(dataDir string) bool {
	_, err := os.Stat(logoPath(dataDir))
	return err == nil
}

// ReadLogo returns the custom logo bytes or the built-in default.
func ReadLogo(dataDir string) ([]byte, error) {
	if b, err := os.ReadFile(logoPath(dataDir)); err == nil {
		return b, nil
	}
	return DefaultLogo(), nil
}

// LogoContentType guesses the image MIME type from file magic, defaulting to SVG
// (the built-in logo) when no raster magic matches.
func LogoContentType(b []byte) string {
	switch {
	case len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8:
		return "image/jpeg"
	case len(b) >= 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G':
		return "image/png"
	default:
		return "image/svg+xml"
	}
}

// SaveLogo validates and writes a PNG/JPEG logo to the data directory.
func SaveLogo(dataDir string, r io.Reader) error {
	b, err := io.ReadAll(io.LimitReader(r, MaxLogoBytes+1))
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return fmt.Errorf("пустой файл")
	}
	if len(b) > MaxLogoBytes {
		return fmt.Errorf("логотип больше %d КБ", MaxLogoBytes>>10)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "png" && format != "jpeg") {
		return fmt.Errorf("нужен PNG или JPEG")
	}
	if cfg.Width > maxLogoDim || cfg.Height > maxLogoDim {
		return fmt.Errorf("изображение больше %d×%d пикселей", maxLogoDim, maxLogoDim)
	}
	if err := os.MkdirAll(brandingDir(dataDir), 0o700); err != nil {
		return err
	}
	return os.WriteFile(logoPath(dataDir), b, 0o600)
}

// DeleteLogo removes the custom logo, reverting to the built-in default.
func DeleteLogo(dataDir string) error {
	err := os.Remove(logoPath(dataDir))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
