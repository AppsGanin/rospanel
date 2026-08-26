package core

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/i18n/dictcheck"
)

// Diagnostics travel to the panel as dictionary keys, so nothing on either side of
// the wire can catch a key the panel has no entry for: the SPA assembles it at
// runtime and would render "health.nodeCertOK" at the operator instead of a
// sentence. This test closes that gap — every key the Go code emits must exist in
// the reference dictionary.
//
// ru.ts is read as text rather than parsed: the keys are plain identifiers on their
// own lines, so a substring check is both sufficient and immune to formatting.
func TestHealthKeysHaveDictionaryEntries(t *testing.T) {
	var src strings.Builder
	for _, f := range []string{"manager_health_report.go", "manager_nodes_health.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src.Write(b)
	}
	keys := regexp.MustCompile(`"(health\.[A-Za-z0-9_]+)"`).FindAllStringSubmatch(src.String(), -1)
	if len(keys) == 0 {
		t.Fatal("no health.* keys found — did the checks stop using dictionary keys?")
	}

	for _, dict := range []string{"ru.ts", "en.ts"} {
		d, err := dictcheck.Load(".", dict)
		if err != nil {
			t.Skipf("frontend dictionary not available (%v)", err)
		}
		seen := map[string]bool{}
		for _, m := range keys {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if _, ok := d.Resolve(m[1]); !ok {
				t.Errorf("%s: %s does not resolve", dict, m[1])
			}
		}
	}
}
