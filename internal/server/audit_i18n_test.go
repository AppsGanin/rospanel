package server

import (
	"os"
	"regexp"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n/dictcheck"
)

// A settings row stores its section as a dictionary key, so nothing on either side
// can catch a section the panel has no entry for — the journal would show the owner
// a raw "audit.sec.nodeGeoCadence". This test closes that gap: every section the
// route table declares must exist in both dictionaries.
func TestAuditSectionsHaveDictionaryEntries(t *testing.T) {
	src, err := os.ReadFile("audit.go")
	if err != nil {
		t.Fatalf("read audit.go: %v", err)
	}
	// The external /v1 mux audits into the same namespace through its own helper, so
	// its sections have to be covered here too — they are read from the same journal.
	api, err := os.ReadFile("api_v1.go")
	if err != nil {
		t.Fatalf("read api_v1.go: %v", err)
	}
	leaves := regexp.MustCompile(`set\("([A-Za-z0-9_]+)"\)`).FindAllStringSubmatch(string(src), -1)
	leaves = append(leaves,
		regexp.MustCompile(`nodeAudit\("[^"]+", "([A-Za-z0-9_]+)"`).FindAllStringSubmatch(string(api), -1)...)
	if len(leaves) == 0 {
		t.Fatal("no sections found — did set() stop taking a dictionary key?")
	}
	for _, dict := range []string{"ru.ts", "en.ts"} {
		d, err := dictcheck.Load(".", dict)
		if err != nil {
			t.Skipf("frontend dictionary not available (%v)", err)
		}
		for _, m := range leaves {
			if _, ok := d.Resolve("audit.sec." + m[1]); !ok {
				t.Errorf("%s: audit.sec.%s does not resolve", dict, m[1])
			}
		}
	}
}
