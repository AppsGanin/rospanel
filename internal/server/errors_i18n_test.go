package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/i18n/dictcheck"
)

// Error codes are assembled at runtime, so nothing on either side of the wire can
// catch one the panel has no entry for. The fallback text stops it rendering as a
// bare key, which is exactly what makes the gap easy to miss: the operator quietly
// gets Russian in an English panel. This test makes it fail instead.
func TestErrorCodesHaveDictionaryEntries(t *testing.T) {
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
	re := regexp.MustCompile(`writeErr(?:Code|Detail)\([^,]+,[^,]+, "err\.([A-Za-z0-9_]+)"`)
	codes := re.FindAllStringSubmatch(src.String(), -1)
	// writeCoded takes the code first (it has no status argument), so it needs its
	// own pattern rather than a wider one that would match anything.
	codes = append(codes, regexp.MustCompile(`writeCoded\([^,]+, "err\.([A-Za-z0-9_]+)"`).
		FindAllStringSubmatch(src.String(), -1)...)
	if len(codes) == 0 {
		t.Fatal("no err.* codes found — did the error writers stop carrying codes?")
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

// A detail-carrying message must have a {{detail}} slot in both dictionaries, or the
// text the outside world gave us (a Telegram refusal, an ACME failure) is silently
// dropped when the code is translated — leaving the operator with "could not send:"
// and nothing after the colon.
func TestDetailErrorsInterpolateTheirDetail(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	var src strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		src.Write(b)
	}
	re := regexp.MustCompile(`writeErrDetail\([^,]+,[^,]+, "err\.([A-Za-z0-9_]+)"`)
	for _, dict := range []string{"ru.ts", "en.ts"} {
		d, err := dictcheck.Load(".", dict)
		if err != nil {
			t.Skipf("frontend dictionary not available (%v)", err)
		}
		for _, m := range re.FindAllStringSubmatch(src.String(), -1) {
			v, ok := d.Resolve("err." + m[1])
			if !ok {
				continue // already reported by the resolution test above
			}
			if !strings.Contains(v, "{{detail}}") {
				t.Errorf("%s: err.%s carries a detail but has no {{detail}} slot", dict, m[1])
			}
		}
	}
}
