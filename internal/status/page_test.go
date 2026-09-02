package status

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/i18n"
)

func render(t *testing.T, d Data) string {
	t.Helper()
	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(out)
}

func themed(t *testing.T, name string, lang i18n.Lang) Data {
	t.Helper()
	var d Data
	d.Theme(name, "", lang, 90)
	return d
}

// Server names are typed by the operator and day titles are built from dates,
// but the page is public: nothing that reaches the template may become markup.
func TestRenderEscapesOperatorText(t *testing.T) {
	d := themed(t, `Acme "VPN"`, i18n.EN)
	d.Servers = []Server{{
		Name: "<script>alert(1)</script>",
		Up:   true,
		Days: []Day{{Class: "up", Title: `x" onmouseover="alert(2)`}},
	}}
	d.UpdatedAt = "<b>now</b>"
	html := render(t, d)

	for _, raw := range []string{"<script>alert(1)</script>", `onmouseover="alert(2)`, "<b>now</b>"} {
		if strings.Contains(html, raw) {
			t.Errorf("unescaped %q reached the page", raw)
		}
	}
	if !strings.Contains(html, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("the server name was not rendered escaped")
	}
	if !strings.Contains(html, "Acme &#34;VPN&#34;") && !strings.Contains(html, "Acme &quot;VPN&quot;") {
		t.Error("the quoted panel name in the title was not escaped")
	}
}

func TestRenderShowsStateUptimeAndHeadline(t *testing.T) {
	d := themed(t, "", i18n.EN)
	d.HeadlineOK = false
	d.UpdatedAt = "12:00"
	d.Servers = []Server{
		{Name: "Amsterdam", Up: true, Uptime: "99.95%", Days: []Day{{Class: "up", Title: "d1"}, {Class: "partial", Title: "d2"}}},
		{Name: "Frankfurt", Up: false, Days: []Day{{Class: "none", Title: "d3"}}},
	}
	html := render(t, d)

	if !strings.Contains(html, d.L.SomeDown) || strings.Contains(html, d.L.AllUp) {
		t.Error("the headline does not reflect an outage")
	}
	if !strings.Contains(html, `class="dot bad"`) {
		t.Error("the headline dot is not red during an outage")
	}
	if !strings.Contains(html, `class="state ok"`) || !strings.Contains(html, `class="state bad"`) {
		t.Error("per-server state classes missing")
	}
	if !strings.Contains(html, d.L.Uptime+" 99.95%") {
		t.Error("the uptime figure is not shown next to its label")
	}
	// A server with no samples says so instead of showing 0.00%, which would read
	// as a total outage.
	if !strings.Contains(html, d.L.NoHistory) {
		t.Error("a server without history did not say so")
	}
	for _, bar := range []string{`class="bar up" title="d1"`, `class="bar partial" title="d2"`, `class="bar none" title="d3"`} {
		if !strings.Contains(html, bar) {
			t.Errorf("bar %s missing", bar)
		}
	}
	if !strings.Contains(html, d.L.Updated+" 12:00") {
		t.Error("the footer timestamp is missing")
	}
	if !strings.Contains(html, `<html lang="en">`) {
		t.Error("the page language is not set")
	}

	d.HeadlineOK = true
	html = render(t, d)
	if !strings.Contains(html, d.L.AllUp) || !strings.Contains(html, `class="dot ok"`) {
		t.Error("the all-clear headline is not rendered")
	}
}

// The page is served to anyone and is read from networks that block CDNs: it
// must be self-contained, unindexed, and must carry nothing beyond what Data
// holds — no address, host or version can leak because none is rendered.
func TestRenderIsSelfContainedAndUnindexed(t *testing.T) {
	d := themed(t, "", i18n.RU)
	d.Servers = []Server{{Name: "Москва", Up: true}}
	html := render(t, d)

	if !strings.Contains(html, `name="robots" content="noindex,nofollow"`) {
		t.Error("the page is indexable")
	}
	if strings.Contains(html, "http://") || strings.Contains(html, "https://") {
		t.Error("the page references another host; a blocked CDN would stall it")
	}
	if strings.Contains(html, `rel="icon"`) {
		t.Error("an icon link is emitted with no logo configured")
	}
	if strings.Contains(html, "<link") && !strings.Contains(html, `rel="icon"`) {
		t.Error("an external stylesheet or resource link appeared")
	}

	d.LogoURL = "/s/logo"
	html = render(t, d)
	if !strings.Contains(html, `<link rel="icon" href="/s/logo" />`) {
		t.Error("the configured logo is not used as the tab icon")
	}
}

// The palette is injected inside <style>; html/template's CSS escaper must let
// a hex colour through, or every page would render with "ZgotmplZ" for colours.
func TestRenderKeepsThePaletteInCSS(t *testing.T) {
	d := themed(t, "", i18n.EN)
	html := render(t, d)
	for _, want := range []string{"--brand: " + d.Brand + ";", "--bg: " + d.Bg + ";", "--ok: " + d.SuccessFg + ";"} {
		if !strings.Contains(html, want) {
			t.Errorf("%q missing from the style block", want)
		}
	}
	if strings.Contains(html, "ZgotmplZ") {
		t.Error("a palette value was rejected by the template escaper")
	}
}

func TestDayClassThresholds(t *testing.T) {
	cases := []struct {
		samples int
		ratio   float64
		want    string
	}{
		{0, 1, "none"}, // nothing recorded beats any ratio
		{0, 0, "none"},
		{10, 1, "up"},
		{10, 0.999, "up"},
		{10, 0.9989, "partial"},
		{10, 0.9, "partial"},
		{10, 0.8999, "down"},
		{10, 0, "down"},
	}
	for _, tc := range cases {
		if got := DayClass(tc.samples, tc.ratio); got != tc.want {
			t.Errorf("DayClass(%d, %v) = %q, want %q", tc.samples, tc.ratio, got, tc.want)
		}
	}
}

// Two decimals is the status-page convention: 99.95% carries information that
// "100%" would round away.
func TestFormatUptime(t *testing.T) {
	for pct, want := range map[float64]string{99.95: "99.95%", 100: "100.00%", 99.999: "100.00%", 0: "0.00%", 87.5: "87.50%"} {
		if got := FormatUptime(pct); got != want {
			t.Errorf("FormatUptime(%v) = %q, want %q", pct, got, want)
		}
	}
}

func TestThemeFillsPaletteTitleAndStrings(t *testing.T) {
	d := themed(t, "", i18n.EN)
	def := branding.DefaultTheme()
	if d.Brand != def.Accent || d.Bg != def.Bg || d.Surface != def.Surface || d.Ink != def.Text || d.Muted != def.Muted {
		t.Errorf("default palette not applied: %+v", d)
	}
	if d.BrandDark != branding.Darken(def.Accent, 0.16) {
		t.Errorf("BrandDark = %s", d.BrandDark)
	}
	// The stock name is translated per language, not stored in one language.
	if d.Title != "RosPanel · Service status" {
		t.Errorf("EN title = %q", d.Title)
	}
	if d.L.Days != "over the last 90 days" {
		t.Errorf("window label = %q", d.L.Days)
	}
	if d.Lang != "en" {
		t.Errorf("Lang = %q", d.Lang)
	}
	if ru := themed(t, "", i18n.RU); !strings.HasPrefix(ru.Title, "РосПанель · ") {
		t.Errorf("RU title = %q", ru.Title)
	}
	if custom := themed(t, "  Acme VPN  ", i18n.EN); custom.Title != "Acme VPN · Service status" {
		t.Errorf("custom title = %q", custom.Title)
	}

	// A dark operator theme needs the status colours lightened to stay readable;
	// a light one has them darkened. Theme must route through branding.Fg so the
	// page agrees with the panel.
	var dark Data
	dark.Theme("", `{"surface":"#101418","bg":"#000000"}`, i18n.EN, 30)
	if dark.SuccessFg != branding.Fg("#059669", "#101418") || dark.SuccessFg == d.SuccessFg {
		t.Errorf("dark-surface success colour = %s, light = %s", dark.SuccessFg, d.SuccessFg)
	}
	if dark.Surface != "#101418" {
		t.Errorf("custom surface not applied: %s", dark.Surface)
	}
}
