package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

func themePage(t *testing.T, files map[string]string) string {
	t.Helper()
	return themePageWith(t, files, false)
}

func themePageWith(t *testing.T, files map[string]string, noPanelStyles bool) string {
	t.Helper()
	m := &ThemeMarkup{Key: t.Name(), Files: map[string][]byte{}, Base: "https://h/s/tok/theme/", Version: "abc"}
	for k, v := range files {
		m.Files[k] = []byte(v)
	}
	u := model.User{ID: 7, Name: "Alice", SubToken: "tok"}
	servers := langTestServers()
	b, err := Page(u, servers[0].Set, servers, Billing{}, Devices{}, Access{Markup: m, NoPanelStyles: noPanelStyles}, true, i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A theme replaces the sections it has and leaves the rest — and the page's
// dialogs and script — as they are; an original stays callable as core:<name>.
func TestThemeReplacesSections(t *testing.T) {
	page := themePage(t, map[string]string{
		"header.html": `<header class="hero"><img src="{{$.Asset "bg.webp"}}" alt=""><h1>Hi, {{.Name}}</h1></header>`,
		"connect.html": `<div class="frame">{{template "core:connect" .}}` +
			`<button type="button" data-action="copy" data-value="{{.SubURL}}">Copy</button>` +
			`{{range .DeepLinks}}<span class="proto">{{.Label}}</span>{{end}}</div>`,
		"bg.webp": "x",
	})
	for _, want := range []string{
		`<header class="hero"><img src="https://h/s/tok/theme/bg.webp?v=abc" alt=""><h1>Hi, Alice</h1></header>`,
		`<div class="frame">`,
		`data-action="copy" data-value="`,
		`<span class="proto">`,
		`/qr.png`,        // core:connect
		`class="badges"`, // a section the theme left alone
		`id="apps"`,      // the dialogs
		`data-action`,    // and the script that runs the buttons
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, `<div class="logo">`) {
		t.Error("the replaced header is still there")
	}
}

// A layout puts the sections in its own order, and leaves out what it does not call.
func TestThemeLayout(t *testing.T) {
	page := themePage(t, map[string]string{
		"layout.html": `<main class="grid">{{template "connect" .}}{{template "header" .}}</main>`,
	})
	connect, header := strings.Index(page, "/qr.png"), strings.Index(page, `<div class="logo">`)
	if connect < 0 || header < 0 || connect > header {
		t.Fatalf("connect at %d, header at %d: want connect first", connect, header)
	}
	if strings.Contains(page, `class="badges"`) {
		t.Error("badges rendered though the layout leaves them out")
	}
}

// An empty section file hides the section: html/template alone would keep the
// original in place of a body that is only blanks.
func TestThemeEmptySectionHides(t *testing.T) {
	page := themePage(t, map[string]string{"header.html": "\n  {{/* none */}}\n"})
	if strings.Contains(page, `<div class="logo">`) {
		t.Fatal("an empty header.html left the original header")
	}
}

// A theme the page cannot render is not rendered: the page is the panel's own.
func TestThemeFallsBack(t *testing.T) {
	page := themePage(t, map[string]string{"header.html": `<script>alert(1)</script>`})
	if strings.Contains(page, "alert(1)") || !strings.Contains(page, `<div class="logo">`) {
		t.Fatal("a refused theme was rendered")
	}
}

func TestCheckTheme(t *testing.T) {
	ok := map[string]string{
		"layout.html":  `<div class="wrap">{{template "header" .}}{{template "core:connect" $}}</div>`,
		"header.html":  `{{with .Access.Churn}}<p>{{.Question}}</p>{{end}}{{range $i, $l := .Links}}{{if $i}}, {{end}}<a href="{{$l.Open}}">{{$l.Proto}}</a>{{end}}<a href="https://example.com/help" target="_blank">?</a>`,
		"devices.html": `{{if .Devices.Show}}<ul>{{range .Devices.List}}<li title="{{.HWID}}">{{.Title}}</li>{{else}}<li>{{$.L.DevicesEmpty}}</li>{{end}}</ul>{{end}}<span style="color: var(--brand)">{{printf "%d" (len .Links)}}</span>`,
		"badges.html":  `<svg viewBox="0 0 24 24"><path d="M5 12l5 5L20 7"/></svg><button type="button" data-action="tab" data-value="pay">Pay</button>`,
	}
	if p := CheckTheme(bytesOf(ok)); len(p) > 0 {
		t.Fatalf("a good theme refused: %v", p)
	}
	for name, src := range map[string]string{
		"not a section":    "",
		"field":            `{{.Nope}}`,
		"nested field":     `{{.Billing.Nope}}`,
		"range over int":   `{{range .UserID}}x{{end}}`,
		"range over lit":   `{{range 1000000000}}x{{end}}`,
		"script":           `<script>alert(1)</script>`,
		"handler":          `<button onclick="alert(1)">x</button>`,
		"js link":          `<a href="javascript:alert(1)">x</a>`,
		"js link split":    `<a href="java{{if .Name}}{{end}}script:alert(1)">x</a>`,
		"spaced js link":   `<a href=" jav&#x09;ascript:alert(1)">x</a>`,
		"external img":     `<img src="https://evil.example/p.gif">`,
		"style url":        `<div style="background: url(https://evil.example/x)"></div>`,
		"style element":    `<style>@import url(https://evil.example/x.css);</style>`,
		"form":             `<form action="https://evil.example"><input name="x"></form>`,
		"meta refresh":     `<meta http-equiv="refresh" content="0;url=https://evil.example">`,
		"iframe":           `<iframe src="https://evil.example"></iframe>`,
		"attr from data":   `<div {{.Name}}>x</div>`,
		"tag from data":    `<{{.Name}}>x`,
		"define":           `{{define "x"}}y{{end}}`,
		"call":             `{{call .Name}}`,
		"unknown template": `{{template "nope" .}}`,
		"template dot":     `{{range .Links}}{{template "core:connect" .}}{{end}}`,
		"bad action":       `<button data-action="eval">x</button>`,
		"missing asset":    `<img src="{{$.Asset "nope.png"}}">`,
		"unclosed attr":    `<div class="{{.Name}}`,
		"svg animate":      `<svg><a><animate attributeName="href" to="javascript:alert(1)"/></a></svg>`,
	} {
		file := "header.html"
		if name == "not a section" {
			file = "nope.html"
		}
		if p := CheckTheme(bytesOf(map[string]string{file: src})); len(p) == 0 {
			t.Errorf("%s: %q passed", name, src)
		}
	}
}

// A section that ends up calling itself is refused at install, not run until the
// stack gives out.
func TestCheckThemeCycle(t *testing.T) {
	p := CheckTheme(bytesOf(map[string]string{"header.html": `{{template "layout" .}}`}))
	if len(p) == 0 || !strings.Contains(p[0], "circle") {
		t.Fatalf("got %v, want a cycle", p)
	}
	p = CheckTheme(bytesOf(map[string]string{"layout.html": `{{template "core:layout" .}}`, "header.html": `<b>{{template "core:header" .}}</b>`}))
	if len(p) > 0 {
		t.Fatalf("wrapping an original refused: %v", p)
	}
}

func bytesOf(m map[string]string) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range m {
		out[k] = []byte(v)
	}
	return out
}

// A page written from scratch: its own layout and dialogs, the panel's stylesheet
// left out (provides.theme.panel_styles = false). Nothing of the panel's markup is
// left but the script that runs it, and the fonts and colours.
func TestThemeWholePage(t *testing.T) {
	page := themePageWith(t, map[string]string{
		"layout.html":  `<main class="mine"><button type="button" data-action="copy" data-value="{{.SubURL}}">x</button><input id="promo-code" type="text"></main>`,
		"dialogs.html": `<div id="apps" class="modal">{{range $i, $d := .DeepLinks}}<a href="{{$d.Href}}" data-i="{{$i}}" data-action="app">{{$d.Label}}</a>{{end}}</div>`,
	}, true)
	for _, gone := range []string{`<div class="logo">`, `class="badges"`, `onclick="openApps()"`, `.wrap {`} {
		if strings.Contains(page, gone) {
			t.Errorf("the panel's %q is still on a page written from scratch", gone)
		}
	}
	for _, want := range []string{`<main class="mine">`, `data-action="app"`, `--brand:`, `@font-face`, `var THEME_ACTS`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestCheckThemeWholePage(t *testing.T) {
	if p := CheckTheme(bytesOf(map[string]string{"styles.html": ``})); len(p) == 0 {
		t.Error("styles.html passed: the panel's stylesheet is switched off in plugin.json")
	}
	if p := CheckTheme(bytesOf(map[string]string{"layout.html": `<input type="password">`})); len(p) == 0 {
		t.Error("a password field passed")
	}
	if p := CheckTheme(bytesOf(map[string]string{"dialogs.html": `{{template "layout" .}}`, "layout.html": `{{template "dialogs" .}}`})); len(p) == 0 {
		t.Error("layout and dialogs calling each other passed")
	}
	ok := map[string]string{
		"dialog-plans.html": `{{range .Billing.Plans}}<select id="per-{{.ID}}" data-action="period" data-plan="{{.ID}}">{{range .Options}}<option value="{{.Periods}}">{{.Label}}</option>{{end}}</select>{{end}}`,
		"layout.html":       `<label>{{if .Billing.AutoRenew}}<input type="checkbox" data-action="auto-renew" checked>{{else}}<input type="checkbox" data-action="auto-renew">{{end}} x</label><b class="a{{if .Online}} on{{end}}" data-linked="{{if .Access.TGLinked}}1{{end}}">x</b>`,
	}
	if p := CheckTheme(bytesOf(ok)); len(p) > 0 {
		t.Fatalf("refused: %v", p)
	}
}

// Each section hides itself when it has nothing to show, so a layout of one's own
// can call any of them without the conditions the panel's layout has around them.
func TestThemeSectionsHideThemselves(t *testing.T) {
	page := themePage(t, map[string]string{"layout.html": `<main>{{template "plan" .}}{{template "referral" .}}{{template "promo" .}}{{template "rotate" .}}</main>`})
	a, b := strings.Index(page, "<main>"), strings.Index(page, "</main>")
	if a < 0 || b < 0 {
		t.Fatal("no layout")
	}
	if inner := strings.TrimSpace(page[a+len("<main>") : b]); inner != "" {
		t.Fatalf("sections with nothing to show rendered: %q", inner)
	}
}

// What gets past a check that reads the markup with every branch at once: the ways
// found reviewing it, each refused.
func TestCheckThemeBypasses(t *testing.T) {
	for name, src := range map[string]string{
		"empty branch joins a name":  `<a {{if .Name}}{{else}}z{{end}}href="javascript:alert(1)">x</a>`,
		"branch glues a name":        `<a {{if .Name}}href{{else}}title{{end}}="javascript:alert(1)">x</a>`,
		"branch drops part of a url": `<a href="java{{if .Name}}XX{{end}}script:alert(1)">x</a>`,
		"branch in a style":          `<b style="color:{{if .Name}}red{{end}}">x</b>`,
		"branch splits an element":   `{{if .Name}}<a href="https://ok/{{else}}<a href="{{end}}javascript:alert(1)">x</a>`,
		"section in an attribute":    `<b title="{{template "core:legal" .}}">x</b>`,
		"section between attrs":      `<b {{template "core:legal" .}}>x</b>`,
		"section in a range":         `{{range .Links}}{{template "core:connect" $}}{{end}}`,
		"three ranges deep":          `{{range .Links}}{{range .Links}}{{range .Links}}{{end}}{{end}}{{end}}`,
		"ends inside a tag":          `<b title="x`,
		"ends between attributes":    `<b `,
		"mark in a comment":          `<!-- {{if .Name}} -->x<!-- {{end}} -->`,
		"name clobbers the script":   `<img name="getElementById" src="{{.SubURL}}/qr.png">`,
		"svg use outside":            `<svg><use href="https://evil.example/s.svg#a"/></svg>`,
		"upper-case script":          `<SCRIPT>alert(1)</SCRIPT>`,
		"split script":               `<scr{{if .Name}}{{end}}ipt>alert(1)</script>`,
		"entity scheme":              `<a href="&#106;avascript:alert(1)">x</a>`,
		"named entity colon":         `<a href="javascript&colon;alert(1)">x</a>`,
		"unquoted scheme":            `<a href=javascript:alert(1)>x</a>`,
		"svg xlink":                  `<svg><a xlink:href="javascript:alert(1)"><text>x</text></a></svg>`,
		"foreignObject":              `<svg><foreignObject><img src=x onerror=alert(1)></foreignObject></svg>`,
		"cdata in svg":               `<svg><![CDATA[<script>alert(1)</script>]]></svg>`,
		"input image":                `<input type="image" src="https://evil.example/x">`,
		"template element":           `<template><script>alert(1)</script></template>`,
		"noscript":                   `<noscript><p title="</noscript><img src=x onerror=alert(1)>"></noscript>`,
		"html/template escaper":      `{{_html_template_htmlescaper .Name}}`,
		"range over an int var":      `{{$n := .UserID}}{{range $n}}x{{end}}`,
	} {
		if p := CheckTheme(bytesOf(map[string]string{"layout.html": src})); len(p) == 0 {
			t.Errorf("%s passed: %s", name, src)
		}
	}
	// Data in an address is html/template's to filter, whatever makes it.
	page := themePage(t, map[string]string{"layout.html": `<a id="t" href="{{printf "%s" "javascript:alert(1)"}}">x</a>`})
	if !strings.Contains(page, `<a id="t" href="#ZgotmplZ">`) {
		t.Error("a script address from data was not filtered")
	}
}
