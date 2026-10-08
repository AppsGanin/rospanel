package manifest

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// A theme restyles the panel's subscription page: theme/theme.css and the files it
// uses (images, fonts) next to it. The page stays the panel's — every button and
// form keeps working — and the theme only changes how it looks. Everything a theme
// shows comes from the panel: a stylesheet that would load from elsewhere is
// refused, since a host blocked in the reader's country hangs the page (and
// tracks who opened it).

const (
	ThemeCSS        = "theme.css"
	maxThemeFile    = 1 << 20
	maxThemeTotal   = 3 << 20
	maxThemeFiles   = 50
	themeFilePrefix = "theme/"
)

var (
	themeFileRe = regexp.MustCompile(`^theme/[a-z0-9][a-z0-9._-]{0,63}\.(css|png|jpg|jpeg|webp|gif|svg|woff2|woff)$`)
	cssURLRe    = regexp.MustCompile(`(?i)url\(\s*(['"]?)([^'")]*)(['"]?)\s*\)`)
	cssComment  = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// ThemeTypes are the content types the panel serves a theme's files with.
var ThemeTypes = map[string]string{
	".css": "text/css; charset=utf-8", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".gif": "image/gif", ".svg": "image/svg+xml", ".woff2": "font/woff2", ".woff": "font/woff",
}

// readTheme collects theme/ files into pkg.Theme (keyed by file name) and checks
// them; Read calls it for names under theme/.
func (pkg *Package) readTheme(p *Problems, name string, b []byte) {
	if !themeFileRe.MatchString(name) {
		p.addf("%s: a theme file is theme/<name>.css|png|jpg|jpeg|webp|gif|svg|woff2|woff (a-z, 0-9, . _ -)", name)
		return
	}
	if len(b) > maxThemeFile {
		p.addf("%s: larger than %d KB", name, maxThemeFile>>10)
		return
	}
	file := strings.TrimPrefix(name, themeFilePrefix)
	if path.Ext(file) == ".css" && file != ThemeCSS {
		p.addf("%s: a theme has one stylesheet, theme/%s", name, ThemeCSS)
		return
	}
	if pkg.Theme == nil {
		pkg.Theme = map[string][]byte{}
	}
	pkg.Theme[file] = b
}

// checkTheme runs once every file is read.
func (pkg *Package) checkTheme(p *Problems) {
	wants := pkg.Manifest != nil && pkg.Manifest.Provides.Theme
	if len(pkg.Theme) == 0 {
		if wants {
			p.addf("provides.theme: theme/%s is missing", ThemeCSS)
		}
		return
	}
	if !wants {
		p.addf(`theme/: add "theme": true to provides`)
	}
	if len(pkg.Theme) > maxThemeFiles {
		p.addf("theme/: at most %d files", maxThemeFiles)
	}
	total := 0
	for _, b := range pkg.Theme {
		total += len(b)
	}
	if total > maxThemeTotal {
		p.addf("theme/: %d KB, at most %d KB together", total>>10, maxThemeTotal>>10)
	}
	css, ok := pkg.Theme[ThemeCSS]
	if !ok {
		p.addf("theme/: theme/%s is missing", ThemeCSS)
		return
	}
	for _, problem := range CheckThemeCSS(string(css), pkg.Theme) {
		p.addf("theme/%s: %s", ThemeCSS, problem)
	}
}

// CheckThemeCSS lists what keeps a stylesheet from being served: anything loaded
// from outside the theme, and the old script hooks of CSS.
func CheckThemeCSS(css string, files map[string][]byte) []string {
	var out []string
	css = cssComment.ReplaceAllString(css, " ") // what a browser reads; /**/ tricks included
	if strings.Contains(css, "/*") {
		out = append(out, "a comment is not closed")
	}
	low := strings.ToLower(css)
	for _, bad := range []string{"@import", "expression(", "javascript:", "behavior:", "-moz-binding", "</style", "image-set(", "://"} {
		if strings.Contains(low, bad) {
			out = append(out, fmt.Sprintf("%q is not allowed", bad))
		}
	}
	// CSS escapes spell anything (u\72l( is url(, @\69mport is @import), and a theme
	// has no use for them that a plain character would not serve.
	if strings.Contains(css, `\`) {
		out = append(out, `a backslash escape is not allowed — write the character itself`)
	}
	// A string can carry an address too (image-set, src()): none may leave the theme.
	for _, q := range []string{`"//`, `'//`} {
		if strings.Contains(css, q) {
			out = append(out, fmt.Sprintf("%s…: an address outside the theme is not allowed", q))
		}
	}
	for _, m := range cssURLRe.FindAllStringSubmatch(css, -1) {
		ref := strings.TrimSpace(m[2])
		lref := strings.ToLower(ref)
		switch {
		case strings.HasPrefix(lref, "data:image/"), strings.HasPrefix(lref, "data:font/"):
		case ref == "":
			out = append(out, "url() is empty")
		case strings.ContainsAny(ref, `:/\?#`) || strings.HasPrefix(ref, "."):
			out = append(out, fmt.Sprintf("url(%s): only a file of the theme, by its name (theme/%s)", ref, path.Base(ref)))
		case files[ref] == nil:
			out = append(out, fmt.Sprintf("url(%s): theme/%s is not in the package", ref, ref))
		}
	}
	return out
}
