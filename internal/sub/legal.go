package sub

import (
	"bytes"
	"html/template"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The operator's legal documents (model.LegalDoc), written in Markdown and served as
// pages of their own under a random segment of the subscription path — reachable by
// whoever was shown the link, before they have an account too (the bot's welcome),
// while the path itself stays as unguessable as the Mini App's.

// legalMD renders the documents. GitHub-flavoured (tables, strikethrough, autolinks);
// raw HTML in the source is dropped, not passed through — html.WithUnsafe is never
// set — so a document cannot carry a script onto a page the panel serves.
var legalMD = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// RenderMarkdown turns a document's Markdown into the HTML its page shows.
func RenderMarkdown(md string) (template.HTML, error) {
	var b bytes.Buffer
	if err := legalMD.Convert([]byte(md), &b); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil // #nosec G203 -- goldmark without WithUnsafe escapes raw HTML
}

// IsLegalPath reports whether seg is the documents' address segment.
func IsLegalPath(set *model.Settings, seg string) bool {
	return set.LegalPath != "" && seg == set.LegalPath
}

// LegalURL is a document's public address, or "" while it has none (no host, or no
// document saved yet). The caller leaves out a document that is empty.
func LegalURL(set *model.Settings, kind string) string {
	if strings.TrimSpace(set.Host) == "" || set.LegalPath == "" {
		return ""
	}
	return "https://" + set.Host + "/" + set.SubPathOr() + "/" + set.LegalPath + "/" + kind
}

// LegalTitle is a document's name in lang.
func LegalTitle(kind string, lang i18n.Lang) string {
	if kind == model.LegalPrivacy {
		return i18n.T(lang, "sub.legalPrivacy")
	}
	return i18n.T(lang, "sub.legalTerms")
}

var legalTpl = template.Must(template.New("legal").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<meta name="robots" content="noindex, nofollow" />
<title>{{.Title}}{{if .Brand}} — {{.Brand}}{{end}}</title>
<style>
  :root { color-scheme: light; }
  body { margin: 0; background: {{.Bg}}; color: {{.Ink}};
    font: 16px/1.6 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  main { max-width: 760px; margin: 0 auto; padding: 28px 18px 48px; }
  .card { background: {{.Surface}}; border-radius: 16px; padding: 24px 22px;
    box-shadow: 0 1px 2px rgba(0,0,0,.06); overflow-wrap: anywhere; }
  .brand { font-weight: 700; color: {{.Accent}}; margin: 0 0 6px; font-size: 14px; }
  h1.title { margin: 0 0 4px; font-size: 24px; line-height: 1.25; }
  .date { color: {{.Muted}}; font-size: 13px; margin: 0 0 20px; }
  .doc h1, .doc h2, .doc h3 { line-height: 1.3; margin: 1.4em 0 .5em; }
  .doc h1 { font-size: 21px; } .doc h2 { font-size: 18px; } .doc h3 { font-size: 16px; }
  .doc p, .doc ul, .doc ol { margin: 0 0 .9em; }
  .doc a { color: {{.Accent}}; }
  .doc table { border-collapse: collapse; width: 100%; margin: 0 0 1em; display: block; overflow-x: auto; }
  .doc th, .doc td { border: 1px solid rgba(127,127,127,.3); padding: 6px 10px; text-align: left; }
  .doc blockquote { margin: 0 0 1em; padding: 2px 14px; border-left: 3px solid {{.Accent}}; color: {{.Muted}}; }
  .doc code { font-family: ui-monospace, Menlo, monospace; font-size: .92em; }
  .doc hr { border: 0; border-top: 1px solid rgba(127,127,127,.3); margin: 1.5em 0; }
</style>
</head>
<body>
<main>
  <div class="card">
    {{if .Brand}}<p class="brand">{{.Brand}}</p>{{end}}
    <h1 class="title">{{.Title}}</h1>
    {{if .Updated}}<p class="date">{{.Updated}}</p>{{end}}
    <div class="doc">{{.Body}}</div>
  </div>
</main>
</body>
</html>
`))

// LegalPage renders a document's page in the operator's colours.
func LegalPage(set *model.Settings, doc model.LegalDoc, lang i18n.Lang, loc *time.Location) ([]byte, error) {
	body, err := RenderMarkdown(doc.Body)
	if err != nil {
		return nil, err
	}
	// The service's name from Settings → Branding; with none set, no name at all —
	// the stock one would name the product to whoever finds the address.
	name := branding.Name(set.PanelName)
	if name == branding.DefaultName {
		name = ""
	}
	updated := ""
	if doc.UpdatedAt > 0 {
		if loc == nil {
			loc = time.UTC
		}
		updated = i18n.T(lang, "sub.legalUpdated", time.Unix(doc.UpdatedAt, 0).In(loc).Format("02.01.2006"))
	}
	theme := branding.ParseTheme(set.PanelTheme)
	var b bytes.Buffer
	err = legalTpl.Execute(&b, map[string]any{
		"Lang": string(lang), "Brand": name, "Title": LegalTitle(doc.Kind, lang), "Updated": updated,
		"Body": body, "Bg": theme.Bg, "Surface": theme.Surface, "Ink": theme.Text,
		"Muted": theme.Muted, "Accent": theme.Accent,
	})
	return b.Bytes(), err
}
