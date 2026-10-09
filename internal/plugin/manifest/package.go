package manifest

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/plugin/pdb"
)

const (
	// MaxPackage bounds the zip as uploaded; MaxUnpacked bounds what it expands
	// to, so a zip bomb is refused by arithmetic rather than by running out of RAM.
	MaxPackage  = 5 << 20
	MaxUnpacked = 12 << 20
	MaxMain     = 2 << 20
	maxFiles    = 200
	maxI18n     = 256 << 10
	maxReadme   = 256 << 10
	maxIcon     = 128 << 10
	maxMigr     = 256 << 10
	maxMigrs    = 100
)

// Package is a read and validated plugin package.
type Package struct {
	Manifest   *Manifest
	Main       string
	Migrations []pdb.Migration
	I18n       map[string]map[string]string // lang → key → text
	Readme     string
	Icon       []byte            // SVG
	Theme      map[string][]byte // theme/ files by name (theme.go)
	SHA256     string            // of the zip as uploaded
	Raw        []byte
}

var (
	migrationRe = regexp.MustCompile(`^migrations/[0-9]{4}[A-Za-z0-9_.-]*\.sql$`)
	i18nRe      = regexp.MustCompile(`^i18n/([a-z]{2})\.json$`)
)

// Read unpacks and validates a plugin zip for this panel version.
//
// The layout is fixed and small: plugin.json and main.js at the root, optional
// migrations/NNNN*.sql, i18n/<lang>.json, README.md, icon.svg, LICENSE and
// CHANGELOG.md. A single top-level folder wrapping all of it (what zipping a
// directory produces) is accepted; macOS's __MACOSX and .DS_Store are skipped;
// anything else is refused, so what the operator reviews is all there is.
func Read(raw []byte, panelVersion string) (*Package, error) {
	if len(raw) > MaxPackage {
		return nil, Problems{fmt.Sprintf("package: larger than %d MB", MaxPackage>>20)}
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, Problems{"package: not a zip archive: " + err.Error()}
	}
	files, err := unpack(zr)
	if err != nil {
		return nil, err
	}

	var p Problems
	pkg := &Package{I18n: map[string]map[string]string{}, Raw: raw}
	sum := sha256.Sum256(raw)
	pkg.SHA256 = hex.EncodeToString(sum[:])

	mf, ok := files["plugin.json"]
	if !ok {
		return nil, Problems{"package: plugin.json missing at the root"}
	}
	m, err := Parse(mf)
	if err != nil {
		return nil, err
	}
	pkg.Manifest = m
	if err := m.Validate(panelVersion); err != nil {
		p = append(p, err.(Problems)...)
	}

	main, ok := files["main.js"]
	switch {
	case !ok:
		p.addf("package: main.js missing at the root")
	case len(main) > MaxMain:
		p.addf("main.js: larger than %d MB", MaxMain>>20)
	case !utf8.Valid(main):
		p.addf("main.js: not UTF-8")
	default:
		pkg.Main = string(main)
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b := files[name]
		switch {
		case name == "plugin.json", name == "main.js", name == "LICENSE", name == "CHANGELOG.md":
		case name == "README.md":
			if len(b) > maxReadme {
				p.addf("README.md: larger than %d KB", maxReadme>>10)
			}
			pkg.Readme = string(b)
		case name == "icon.svg":
			if len(b) > maxIcon {
				p.addf("icon.svg: larger than %d KB", maxIcon>>10)
			}
			pkg.Icon = b
		case migrationRe.MatchString(name):
			if len(b) > maxMigr {
				p.addf("%s: larger than %d KB", name, maxMigr>>10)
			}
			if err := pdb.CheckSQL(string(b)); err != nil {
				p.addf("%s: %v", name, err)
			}
			pkg.Migrations = append(pkg.Migrations, pdb.Migration{Name: path.Base(name), SQL: string(b)})
		case i18nRe.MatchString(name):
			lang := i18nRe.FindStringSubmatch(name)[1]
			var dict map[string]string
			if len(b) > maxI18n {
				p.addf("%s: larger than %d KB", name, maxI18n>>10)
			} else if err := json.Unmarshal(b, &dict); err != nil {
				p.addf("%s: must be a JSON object of strings: %v", name, err)
			} else {
				pkg.I18n[lang] = dict
			}
		case strings.HasPrefix(name, themeFilePrefix):
			pkg.readTheme(&p, name, b)
		default:
			p.addf("%s: not part of a plugin package (plugin.json, main.js, migrations/NNNN*.sql, i18n/<lang>.json, theme/*, README.md, icon.svg, LICENSE, CHANGELOG.md)", name)
		}
	}
	pkg.checkTheme(&p)
	if len(pkg.Migrations) > maxMigrs {
		p.addf("migrations: at most %d", maxMigrs)
	}
	if len(p) > 0 {
		return nil, p
	}
	return pkg, nil
}

// Unpack reads a zip's files safely — the same checks as Read, without asking
// them to be a package: a plugin's sources carried to and from the panel.
func Unpack(raw []byte) (map[string][]byte, error) {
	if len(raw) > MaxPackage {
		return nil, Problems{fmt.Sprintf("zip: larger than %d MB", MaxPackage>>20)}
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, Problems{"zip: not a zip archive: " + err.Error()}
	}
	return unpack(zr)
}

// unpack reads every file of the zip into memory, checking names and sizes as it
// goes, and strips one common top-level folder.
func unpack(zr *zip.Reader) (map[string][]byte, error) {
	var entries []*zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || skipped(f.Name) {
			continue
		}
		entries = append(entries, f)
	}
	if len(entries) > maxFiles {
		return nil, Problems{fmt.Sprintf("package: more than %d files", maxFiles)}
	}
	prefix := commonFolder(entries)
	out := map[string][]byte{}
	var total int64
	for _, f := range entries {
		name := strings.TrimPrefix(f.Name, prefix)
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") ||
			path.Clean(name) != name || strings.HasPrefix(name, "../") || name == ".." {
			return nil, Problems{fmt.Sprintf("package: unsafe path %q", f.Name)}
		}
		if f.Mode()&^0o777 != 0 { // symlinks and other specials
			return nil, Problems{fmt.Sprintf("package: %s is not a regular file", f.Name)}
		}
		if _, dup := out[name]; dup {
			return nil, Problems{fmt.Sprintf("package: %s appears twice", name)}
		}
		// The declared size is checked first, then enforced while reading: a zip
		// can lie about its sizes.
		if f.UncompressedSize64 > MaxUnpacked || total+int64(f.UncompressedSize64) > MaxUnpacked {
			return nil, Problems{fmt.Sprintf("package: unpacks to more than %d MB", MaxUnpacked>>20)}
		}
		rc, err := f.Open()
		if err != nil {
			return nil, Problems{fmt.Sprintf("package: %s: %v", name, err)}
		}
		b, err := io.ReadAll(io.LimitReader(rc, MaxUnpacked-total+1))
		_ = rc.Close()
		if err != nil {
			return nil, Problems{fmt.Sprintf("package: %s: %v", name, err)}
		}
		total += int64(len(b))
		if total > MaxUnpacked {
			return nil, Problems{fmt.Sprintf("package: unpacks to more than %d MB", MaxUnpacked>>20)}
		}
		out[name] = b
	}
	return out, nil
}

func skipped(name string) bool {
	return strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store"
}

// commonFolder is "dir/" when every entry sits under the same top-level folder
// and there is no plugin.json at the root — the shape zipping a folder produces.
func commonFolder(entries []*zip.File) string {
	if len(entries) == 0 {
		return ""
	}
	first, _, ok := strings.Cut(entries[0].Name, "/")
	if !ok {
		return ""
	}
	for _, f := range entries {
		if !strings.HasPrefix(f.Name, first+"/") {
			return ""
		}
	}
	return first + "/"
}

// Translate looks a key up in the package's dictionaries: lang, then English, then
// Russian, then the key itself. {name} placeholders are filled from params.
//
// One pass over the template: a value put in is never read again for placeholders
// (else {a} → "{b}{b}…" and a huge b multiply), and the result is held to
// MaxTranslated bytes as it is written — running out of memory here ends the
// panel, not the plugin. Too big filled in, the template is returned as it is.
func (pkg *Package) Translate(lang, key string, params map[string]string) string {
	s := key
	for _, l := range []string{lang, "en", "ru"} {
		if v, ok := pkg.I18n[l][key]; ok {
			s = v
			break
		}
	}
	if len(s) > MaxTranslated {
		s = s[:MaxTranslated]
	}
	if len(params) == 0 || !strings.Contains(s, "{") {
		return s
	}
	var b strings.Builder
	rest := s
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			b.WriteString(rest)
			break
		}
		end := strings.IndexByte(rest[open+1:], '}')
		if end < 0 {
			b.WriteString(rest)
			break
		}
		name := rest[open+1 : open+1+end]
		v, ok := params[name]
		if !ok {
			// Not a placeholder of these params: kept, and the scan goes on after "{".
			b.WriteString(rest[:open+1])
			rest = rest[open+1:]
			continue
		}
		if b.Len()+open+len(v) > MaxTranslated {
			return s
		}
		b.WriteString(rest[:open])
		b.WriteString(v)
		rest = rest[open+1+end+1:]
	}
	if b.Len() > MaxTranslated {
		return s
	}
	return b.String()
}

// MaxTranslated bounds what panel.t returns.
const MaxTranslated = 64 << 10
