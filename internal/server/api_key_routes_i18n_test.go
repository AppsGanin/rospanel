package server

import (
	"os"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/mcp"
)

// Every method a key can be ticked for is named in both panel dictionaries, and so
// is every section it is listed under: a route added to /v1 must not reach the key
// editor as a bare dictionary key.
func TestKeyRoutesAreNamed(t *testing.T) {
	t.Parallel()
	for _, lang := range []string{"ru", "en"} {
		raw, err := os.ReadFile("../../web/src/i18n/" + lang + ".ts")
		if err != nil {
			t.Fatal(err)
		}
		dict := string(raw)
		routes := section(t, dict, "apiRoute")
		tags := section(t, dict, "apiTag")
		for _, r := range tickableRoutes() {
			if key := mcp.ToolName(r.method, r.path); !strings.Contains(routes, "\n    "+key+": ") {
				t.Errorf("%s.ts: apiRoute.%s (%s) is missing", lang, key, r.pattern)
			}
			if !strings.Contains(tags, "\n    "+r.tag+": ") {
				t.Errorf("%s.ts: apiTag.%s is missing", lang, r.tag)
			}
		}
	}
}

// section is the text of one top-level dictionary block.
func section(t *testing.T, dict, name string) string {
	t.Helper()
	start := strings.Index(dict, "\n  "+name+": {")
	if start < 0 {
		t.Fatalf("no %s block", name)
	}
	end := strings.Index(dict[start:], "\n  },")
	if end < 0 {
		t.Fatalf("%s block does not end", name)
	}
	return dict[start : start+end]
}
