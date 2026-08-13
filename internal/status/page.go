// Package status renders the public status page: which servers are up, and how
// they have behaved over the retention window.
//
// It is the only surface the panel exposes to someone holding no token at all, so
// it is deliberately thin. What it shows is a name, a state and a bar chart; what
// it never shows is hosts, addresses, versions, user counts or traffic — anything
// that would help someone map the installation rather than answer "is it me or
// them". The page is off unless an operator turns it on.
package status

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/i18n"
)

//go:embed page.html
var pageHTML string

var pageTmpl = template.Must(template.New("status").Parse(pageHTML))

// Server is one row on the page.
type Server struct {
	Name   string
	Up     bool
	Uptime string // formatted percentage, "" when nothing was sampled yet
	Days   []Day
}

// Day is one bar: a class picking its colour and a title a cursor reveals.
type Day struct {
	Class string // up | partial | down | none
	Title string
}

// Data is everything the template renders.
type Data struct {
	Lang      string
	Title     string
	LogoURL   string
	Brand     string
	BrandDark string
	Ink       string
	Muted     string
	Bg        string
	Surface   string
	SuccessFg string
	DangerFg  string
	WarningFg string

	Headline    string
	HeadlineOK  bool
	Servers     []Server
	WindowLabel string
	UpdatedAt   string
	L           text
}

// text is the page's localised strings, a struct rather than a map so a typo is a
// template error rather than an empty span.
type text struct {
	Title     string
	AllUp     string
	SomeDown  string
	Up        string
	Down      string
	Uptime    string
	NoHistory string
	Updated   string
	Days      string
}

func texts(lang i18n.Lang, days int) text {
	t := func(k string) string { return i18n.T(lang, k) }
	return text{
		Title:     t("status.title"),
		AllUp:     t("status.allUp"),
		SomeDown:  t("status.someDown"),
		Up:        t("status.up"),
		Down:      t("status.down"),
		Uptime:    t("status.uptime"),
		NoHistory: t("status.noHistory"),
		Updated:   t("status.updated"),
		Days:      i18n.T(lang, "status.window", days),
	}
}

// DayClass maps a day's sampled ratio to the bar's colour class. The thresholds
// are the ones a reader expects: a day is green only if it was green all day,
// amber for a dip, red for a real outage, and neutral when nothing was recorded
// (before the panel was installed, or while it was itself down).
func DayClass(samples int, ratio float64) string {
	switch {
	case samples == 0:
		return "none"
	case ratio >= 0.999:
		return "up"
	case ratio >= 0.9:
		return "partial"
	default:
		return "down"
	}
}

// FormatUptime renders an uptime percentage with two decimals, the convention
// every status page uses (99.95% says something 100% rounding would hide).
func FormatUptime(pct float64) string { return fmt.Sprintf("%.2f%%", pct) }

// Render produces the page HTML.
func Render(d Data) ([]byte, error) {
	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Theme fills the palette fields from the panel's branding theme, so the status
// page looks like the operator's panel and subscription page rather than a
// third-party service.
func (d *Data) Theme(panelName, panelTheme string, lang i18n.Lang, days int) {
	name := branding.Name(panelName)
	if name == branding.DefaultName {
		name = i18n.T(lang, "sub.defaultBrand")
	}
	th := branding.ParseTheme(panelTheme)
	d.Brand = th.Accent
	d.BrandDark = branding.Darken(th.Accent, 0.16)
	d.Ink = th.Text
	d.Muted = th.Muted
	d.Bg = th.Bg
	d.Surface = th.Surface
	d.SuccessFg = branding.Fg("#059669", th.Surface)
	d.DangerFg = branding.Fg("#dc2626", th.Surface)
	d.WarningFg = branding.Fg("#ea580c", th.Surface)
	d.Lang = string(lang)
	d.L = texts(lang, days)
	d.Title = strings.TrimSpace(name + " · " + d.L.Title)
}
