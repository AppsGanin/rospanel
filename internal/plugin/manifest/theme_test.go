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
		"theme/theme.css": `/* url() in a comment */ :root { --bg: #000; } body { background: url("bg.png") , url(data:image/png;base64,AA==); }`,
		"theme/bg.png":    "png",
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
	} {
		_, err := Read(themeZip(t, c.provides, c.files), "")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want %q, got %v", c.files, c.want, err)
		}
	}
}
