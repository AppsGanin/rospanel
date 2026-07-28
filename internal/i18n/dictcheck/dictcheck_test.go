package dictcheck

import "testing"

// The parser is what every other parity test now trusts, so it is checked against
// the two shapes that broke earlier attempts at it: a // inside a string value, and
// the `const en: Dict =` type annotation.
func TestParseHandlesTheTrickyShapes(t *testing.T) {
	const src = `
// a leading comment
const en: Dict = {
  common: {
    refresh: "Refresh",
  },
  err: {
    // a comment between entries
    urlScheme: "the URL must start with http:// or https://",
    quoted: "he said \"no\"",
  },
  audit: {
    sec: {
      update: "API · update",
    },
  },
  nodes: {
    update: "Update",
  },
}
`
	d := Parse(src)
	for _, tc := range []struct{ key, want string }{
		{"common.refresh", "Refresh"},
		{"err.urlScheme", "the URL must start with http:// or https://"},
		{"err.quoted", `he said "no"`},
		{"audit.sec.update", "API · update"},
		{"nodes.update", "Update"},
	} {
		got, ok := d.Resolve(tc.key)
		if !ok || got != tc.want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", tc.key, got, ok, tc.want)
		}
	}
	// The whole point: a leaf under the wrong parent must NOT resolve. A substring
	// search for "update:" would have accepted both of these.
	for _, miss := range []string{"err.update", "audit.sec.refresh", "nodes.urlScheme", "common.nope"} {
		if _, ok := d.Resolve(miss); ok {
			t.Errorf("Resolve(%q) resolved, want a miss", miss)
		}
	}
	if len(d) != 4 {
		t.Errorf("top-level sections = %d, want 4 (annotation nested the file?)", len(d))
	}
}

// Load must find the real dictionaries from a package directory.
func TestLoadFindsTheDictionaries(t *testing.T) {
	for _, name := range []string{"ru.ts", "en.ts"} {
		d, err := Load(".", name)
		if err != nil {
			t.Skipf("frontend dictionary not available (%v)", err)
		}
		if _, ok := d.Resolve("common.refresh"); !ok {
			t.Errorf("%s: common.refresh did not resolve", name)
		}
	}
}
