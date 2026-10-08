package manifest

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
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
// from outside the theme, and the old script hooks of CSS. It reads the CSS the
// way a browser does — comments dropped, escapes decoded (u\72l( is url(, @\69mport
// is @import) — so neither hides anything from it.
func CheckThemeCSS(css string, files map[string][]byte) []string {
	var out []string
	css = cssComment.ReplaceAllString(css, " ")
	if strings.Contains(css, "/*") {
		out = append(out, "a comment is not closed")
	}
	css = unescapeCSS(css)
	refs, rest := cssURLs(css)
	low := strings.ToLower(rest)
	for _, bad := range []string{"@import", "expression(", "javascript:", "behavior:", "-moz-binding", "</style", "image-set(", "://"} {
		if strings.Contains(low, bad) {
			out = append(out, fmt.Sprintf("%q is not allowed", bad))
		}
	}
	// A string can carry an address too (src(), a @font-face src): none may leave
	// the theme.
	for _, q := range []string{`"//`, `'//`} {
		if strings.Contains(rest, q) {
			out = append(out, fmt.Sprintf("%s…: an address outside the theme is not allowed", q))
		}
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		lref := strings.ToLower(ref)
		switch {
		case strings.HasPrefix(lref, "data:image/"), strings.HasPrefix(lref, "data:font/"):
			// Inline: an SVG's xmlns="http://…" is a name, not a load.
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

// unescapeCSS decodes CSS escapes: a backslash and 1-6 hex digits (one whitespace
// after them is part of the escape), a backslash and a newline (nothing), or a
// backslash and any other character (that character).
func unescapeCSS(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		j := i
		for j < len(s) && j-i < 6 && isHex(s[j]) {
			j++
		}
		if j > i {
			r, _ := strconv.ParseUint(s[i:j], 16, 32)
			if r == 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF) {
				r = 0xFFFD
			}
			b.WriteRune(rune(r))
			if j < len(s) && strings.IndexByte(" \t\n\r\f", s[j]) >= 0 {
				j++
			}
			i = j - 1
			continue
		}
		if s[i] == '\n' || s[i] == '\f' {
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// cssURLs finds every url(…) — quoted with either quote, or bare up to ")" — and
// returns their contents and the CSS with each url(…) blanked, for the checks
// that look at the rest.
func cssURLs(css string) (refs []string, rest string) {
	var b strings.Builder
	low := strings.ToLower(css)
	i := 0
	for {
		k := strings.Index(low[i:], "url(")
		if k < 0 {
			b.WriteString(css[i:])
			return refs, b.String()
		}
		start := i + k
		b.WriteString(css[i:start])
		j := start + len("url(")
		for j < len(css) && strings.IndexByte(" \t\n\r\f", css[j]) >= 0 {
			j++
		}
		var ref string
		if j < len(css) && (css[j] == '"' || css[j] == '\'') {
			q := css[j]
			end := strings.IndexByte(css[j+1:], q)
			if end < 0 {
				ref, j = css[j+1:], len(css)
			} else {
				ref, j = css[j+1:j+1+end], j+1+end+1
			}
			if close := strings.IndexByte(css[j:], ')'); close >= 0 {
				j += close + 1
			} else {
				j = len(css)
			}
		} else {
			end := strings.IndexByte(css[j:], ')')
			if end < 0 {
				ref, j = css[j:], len(css)
			} else {
				ref, j = css[j:j+end], j+end+1
			}
		}
		refs = append(refs, ref)
		b.WriteString("url()")
		i = j
	}
}
