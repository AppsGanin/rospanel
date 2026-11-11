package manifest

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func themeZip(t *testing.T, provides string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	all := map[string]string{
		"plugin.json": `{"id": "dark", "version": "1.0.0", "api": 1, "name": "Dark", "provides": {` + provides + `}}`,
		"main.js":     "export {};",
	}
	for k, v := range files {
		all[k] = v
	}
	for name, body := range all {
		f, _ := w.Create(name)
		_, _ = f.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestThemePackage(t *testing.T) {
	good := map[string]string{
		"theme/theme.css": `/* url() in a comment */ :root { --bg: #000; } body { background: url("bg.png") , url(data:image/png;base64,AA==); }
			a::before { content: "\2014\00a0"; }
			i { background: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg'><path d='M0 0'/></svg>"); }`,
		"theme/bg.png": "png",
	}
	pkg, err := Read(themeZip(t, `"theme": true`, good), "")
	if err != nil {
		t.Fatal(err)
	}
	if string(pkg.Theme["bg.png"]) != "png" || pkg.Theme[ThemeCSS] == nil {
		t.Fatalf("theme files: %v", pkg.Theme)
	}
	for _, c := range []struct {
		provides string
		files    map[string]string
		want     string
	}{
		{`"theme": true`, nil, "theme.css is missing"},
		{`"events": ["user.created"]`, good, `add "theme": true`},
		{`"theme": true`, map[string]string{"theme/theme.css": `@import url("https://fonts.example/x.css");`}, "@import"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { background: url(https://cdn.example/a.png) }`}, "only a file of the theme"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { background: url(//cdn.example/a.png) }`}, "only a file of the theme"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { background: url(../x.png) }`}, "only a file of the theme"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { background: url(missing.png) }`}, "not in the package"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a{}`, "theme/other.css": `b{}`}, "one stylesheet"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a{}`, "theme/x.js": `alert(1)`}, "a theme file is"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { width: expression(alert(1)) }`}, "expression("},
		{`"theme": true`, map[string]string{"theme/theme.css": `/**/@import "x.css";`}, "@import"},
		{`"theme": true`, map[string]string{"theme/theme.css": `/**/@im/**/port url(x.css);`}, "not in the package"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a{} /* open`}, "not closed"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { background: image-set("https://evil/t.png" 1x) }`}, "image-set("},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { background: u\72l(https://evil/x) }`}, "only a file of the theme"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { background: u\72 l(https://evil/x) }`}, "only a file of the theme"},
		{`"theme": true`, map[string]string{"theme/theme.css": `@\69mport "https://evil/x.css";`}, "@import"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { background: url("ok.png"), url('https://evil/x') }`}, "only a file of the theme"},
		{`"theme": true`, map[string]string{"theme/theme.css": `a { background: url("data:image/svg+xml,x") ; b: "http://evil" }`}, "://"},
		{`"theme": true`, map[string]string{"theme/theme.css": `@font-face { src: "//evil/f.woff2" }`}, "outside the theme"},
	} {
		_, err := Read(themeZip(t, c.provides, c.files), "")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want %q, got %v", c.files, c.want, err)
		}
	}
}

// Characters whose lowering changes their length (K, the Kelvin sign) or broken
// bytes must not shift where url(…) is read: the outside address is still found,
// and nothing panics.
func TestThemeCSSLoweringShift(t *testing.T) {
	files := map[string][]byte{"x.png": {1}}
	bypass := `.a::before{content:"` + strings.Repeat("K", 11) + `"}.b{--v:x.png);background:url(//evil.example/a.png)}`
	if p := CheckThemeCSS(bypass, files); len(p) == 0 {
		t.Fatal("an outside address got through")
	}
	if p := CheckThemeCSS(strings.Repeat("\xff", 40)+"url(x.png)", files); len(p) != 1 || !strings.Contains(p[0], "UTF-8") {
		t.Fatalf("%v", p)
	}
	if p := CheckThemeCSS(`.k{background:URL(x.png)}`, files); len(p) != 0 {
		t.Fatalf("an upper-case url() of the theme: %v", p)
	}
}

// A theme can replace sections of the page with its own markup: checked at install
// against the page, and only with the users' consent, since it renders their page.
func TestThemeMarkupPackage(t *testing.T) {
	zipWith := func(perms string, files map[string]string) []byte {
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		files["plugin.json"] = `{"id": "hero", "version": "1.0.0", "api": 1, "name": "Hero", "permissions": [` + perms + `], "provides": {"theme": true}}`
		files["main.js"] = "export {};"
		for name, body := range files {
			f, _ := w.Create(name)
			_, _ = f.Write([]byte(body))
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	header := `<header class="hero"><h1>{{.Name}}</h1></header>`
	pkg, err := Read(zipWith(`"users.view"`, map[string]string{"theme/header.html": header}), "")
	if err != nil {
		t.Fatal(err) // no theme.css: the markup alone is a theme
	}
	if string(pkg.Theme["header.html"]) != header {
		t.Fatalf("theme files: %v", pkg.Theme)
	}
	for _, c := range []struct {
		perms string
		files map[string]string
		want  string
	}{
		{``, map[string]string{"theme/header.html": header}, `add "users.view"`},
		{`"users.view"`, map[string]string{"theme/header.html": `{{.Nope}}`}, "theme/header.html: .Nope"},
		{`"users.view"`, map[string]string{"theme/header.html": `<script>x</script>`}, "<script> is not allowed"},
		{`"users.view"`, map[string]string{"theme/hero.html": header}, "not a section of the page"},
	} {
		_, err := Read(zipWith(c.perms, c.files), "")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want %q, got %v", c.files, c.want, err)
		}
	}
}

// provides.theme is true, or an object that switches the panel's stylesheet off; a
// typo in it is refused like any other field of plugin.json.
func TestThemeProvidesForms(t *testing.T) {
	for _, c := range []struct {
		json        string
		on, styles  bool
		wantErr     bool
		marshalBack string
	}{
		{`true`, true, true, false, `true`},
		{`false`, false, false, false, `false`},
		{`{}`, true, true, false, `true`},
		{`{"panel_styles": false}`, true, false, false, `{"panel_styles":false}`},
		{`{"panel_styles": true}`, true, true, false, `true`},
		{`{"panel_style": false}`, false, false, true, ``},
		{`"yes"`, false, false, true, ``},
	} {
		var th Theme
		err := th.UnmarshalJSON([]byte(c.json))
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err %v", c.json, err)
			continue
		}
		if c.wantErr {
			continue
		}
		if th.On != c.on || th.PanelStyles != c.styles {
			t.Errorf("%s: got %+v", c.json, th)
		}
		if b, _ := th.MarshalJSON(); string(b) != c.marshalBack {
			t.Errorf("%s: marshals to %s", c.json, b)
		}
	}
}
