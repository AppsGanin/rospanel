package model

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n/dictcheck"
)

// Model validation errors reach the panel as codes (core re-wraps them), assembled
// at runtime — so nothing catches a code with no dictionary entry. The fallback text
// keeps it from rendering as a bare key, which is exactly what hides the gap: the
// operator quietly gets Russian in an English panel.
func TestFieldErrorCodesHaveDictionaryEntries(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// branding sits below core too and raises FieldErrors through the exported
	// constructor, so its codes are on the same wire and belong in the same check.
	files = append(files, filepath.Join("..", "branding", "branding.go"))
	var src strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src.Write(b)
	}
	codes := regexp.MustCompile(`(?:model\.)?[Ff]ieldErr\("err\.([A-Za-z0-9_]+)"`).FindAllStringSubmatch(src.String(), -1)
	if len(codes) == 0 {
		t.Fatal("no err.* codes found — did fieldErr stop being used?")
	}
	for _, dict := range []string{"ru.ts", "en.ts"} {
		d, err := dictcheck.Load(".", dict)
		if err != nil {
			t.Skipf("frontend dictionary not available (%v)", err)
		}
		seen := map[string]bool{}
		for _, m := range codes {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if _, ok := d.Resolve("err." + m[1]); !ok {
				t.Errorf("%s: err.%s does not resolve", dict, m[1])
			}
		}
	}
}
