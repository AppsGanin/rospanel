package payments

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/i18n/dictcheck"
)

// A provider's form is described here and rendered by the panel, so its field
// labels, one-line notes and help texts travel as dictionary keys. td() falls back
// to the raw string, which is what lets a brand name ("PayPalych") sit in the same
// field as a key — and also what would let a typo'd key reach the screen looking
// like a key. This test is the only thing standing between the two.
func TestProviderLabelKeysHaveDictionaryEntries(t *testing.T) {
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
	keys := regexp.MustCompile(`"pay(?:Field|Note|Help)\.([A-Za-z0-9_]+)"`).
		FindAllStringSubmatch(src.String(), -1)
	if len(keys) == 0 {
		t.Fatal("no payField/payNote/payHelp keys found — did the registry stop using them?")
	}
	for _, dict := range []string{"ru.ts", "en.ts"} {
		d, err := dictcheck.Load(".", dict)
		if err != nil {
			t.Skipf("frontend dictionary not available (%v)", err)
		}
		seen := map[string]bool{}
		for _, m := range keys {
			if seen[m[0]] {
				continue
			}
			seen[m[0]] = true
			if _, ok := d.Resolve(strings.Trim(m[0], `"`)); !ok {
				t.Errorf("%s: %s does not resolve", dict, m[0])
			}
		}
	}
}
