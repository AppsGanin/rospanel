package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// callKey matches a T/TN call with a literal key. Calls that pass a variable are
// skipped on purpose: their key is only known at runtime, and the packages that do
// it (the abuse category, the registration decision) carry their own check.
var callKey = regexp.MustCompile(`\bi18n\.TN?\([^,]+, "([a-zA-Z0-9_.]+)"`)

// T falls back to the key itself when the catalog has no entry, so a typo does not
// crash or log — it just puts "notify.abuseNode" in a Telegram message to the
// operator. Nothing else in the toolchain can see that: the key is a string, built
// and looked up at runtime. This test walks every call site in the repository and
// fails on a key the reference catalog does not have.
//
// It caught a real one: abuse categories are keyed off the category constant, and
// the catalog had been written with "abuse.badIP" while the constant spells it
// "badip".
func TestEveryCallSiteKeyExists(t *testing.T) {
	root := filepath.Join("..", "..")
	var missing []string
	seen := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "web", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range callKey.FindAllStringSubmatch(string(b), -1) {
			key := m[1]
			if seen[key] {
				continue
			}
			seen[key] = true
			if _, ok := ru[key]; ok {
				continue
			}
			// A plural key is passed to TN by its base; the forms live under it.
			if _, ok := ru[key+"_one"]; ok {
				continue
			}
			missing = append(missing, key+" ("+path+")")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("no call sites found — did the T/TN signature change?")
	}
	for _, m := range missing {
		t.Errorf("catalog has no entry for %s", m)
	}
}
