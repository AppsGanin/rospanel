package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/i18n/dictcheck"
)

// Validation errors travel to the panel as codes, assembled at runtime, so neither
// the compiler nor the type checker can see that a code has no dictionary entry.
// The fallback text keeps such a case from rendering as a bare key, but it also
// makes the gap invisible: the operator silently gets Russian in an English panel.
// This test is what makes it visible.
func TestValidationCodesHaveDictionaryEntries(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
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
	codes := regexp.MustCompile(`invalidCode\("err\.([A-Za-z0-9_]+)"`).FindAllStringSubmatch(src.String(), -1)
	if len(codes) == 0 {
		t.Fatal("no err.* codes found — did invalidCode stop being used?")
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
