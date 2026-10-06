// Package devkit is what `rospanel plugin …` runs for plugin authors: a new
// plugin from a template, packing a directory into an installable zip, checking it
// the way the panel will, running its test.js, and a REPL to try it by hand. It
// runs the real host (internal/plugin) on a temporary directory, so what passes
// here behaves the same inside a panel — no Node, no second runtime.
package devkit

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

//go:embed template
var templateFS embed.FS

var idRe = regexp.MustCompile(`^[a-z][a-z0-9-]{2,39}$`)

// New writes a new plugin from the template into dir (which must not exist or be
// empty). id names the plugin; panelVersion fills "panel": ">=…".
func New(dir, id, panelVersion string) ([]string, error) {
	if !idRe.MatchString(id) {
		return nil, fmt.Errorf("id %q: 3-40 characters of a-z, 0-9 and -, starting with a letter", id)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s is not empty", dir)
	}
	name := strings.ToUpper(id[:1]) + strings.ReplaceAll(id[1:], "-", " ")
	var written []string
	err := fs.WalkDir(templateFS, "template", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "template/")
		b, err := templateFS.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(rel, ".tmpl") {
			rel = strings.TrimSuffix(rel, ".tmpl")
			b = []byte(strings.NewReplacer("{{ID}}", id, "{{NAME}}", name, "{{PANEL}}", panelVersion).Replace(string(b)))
		}
		out := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		written = append(written, rel)
		return os.WriteFile(out, b, 0o644)
	})
	sort.Strings(written)
	return written, err
}

// packaged reports whether a file of a plugin directory goes into the package.
// The package layout is fixed (see manifest.Read); everything else — test.js,
// rospanel.d.ts, sources a bundler built main.js from — stays out.
func packaged(rel string) bool {
	switch rel {
	case "plugin.json", "main.js", "README.md", "icon.svg", "LICENSE", "CHANGELOG.md":
		return true
	}
	dir, file := path.Split(rel)
	switch dir {
	case "migrations/":
		return strings.HasSuffix(file, ".sql")
	case "i18n/":
		return strings.HasSuffix(file, ".json")
	}
	return false
}

// Pack zips a plugin directory. skipped lists the files left out.
func Pack(dir string) (raw []byte, skipped []string, err error) {
	var files []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if base := d.Name(); rel != "." && (strings.HasPrefix(base, ".") || base == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case packaged(rel):
			files = append(files, rel)
		case strings.HasSuffix(rel, ".zip"): // an earlier pack's output
		default:
			skipped = append(skipped, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, nil, err
		}
		// No timestamp: the same sources make the same zip, so its sha256 — what the
		// consent screen and a catalog pin — depends on the content only.
		hdr := &zip.FileHeader{Name: rel, Method: zip.Deflate}
		f, err := w.CreateHeader(hdr)
		if err != nil {
			return nil, nil, err
		}
		if _, err := f.Write(b); err != nil {
			return nil, nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), skipped, nil
}

// ReadPackage loads what `validate`, `test` and `dev` work on: a directory (packed
// on the fly) or a .zip.
func ReadPackage(src string) ([]byte, error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		raw, _, err := Pack(src)
		return raw, err
	}
	return os.ReadFile(src)
}

// CheckMigrations refuses a new package whose migrations rewrite ones the previous
// version shipped: a panel that applied the old text never runs the new one.
func CheckMigrations(prev, next *manifest.Package) error {
	old := map[string][32]byte{}
	for _, m := range prev.Migrations {
		old[m.Name] = sha256.Sum256([]byte(m.SQL))
	}
	var bad []string
	have := map[string]bool{}
	for _, m := range next.Migrations {
		have[m.Name] = true
		if h, ok := old[m.Name]; ok && h != sha256.Sum256([]byte(m.SQL)) {
			bad = append(bad, m.Name+" was changed")
		}
	}
	for name := range old {
		if !have[name] {
			bad = append(bad, name+" was removed")
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return errors.New("migrations: " + strings.Join(bad, ", ") + " — add a new migration instead")
	}
	return nil
}
