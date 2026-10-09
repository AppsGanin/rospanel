package manifest

import (
	"strings"
	"testing"
)

// A template that would fill in to gigabytes is returned as it is.
func TestTranslateBounded(t *testing.T) {
	pkg := &Package{I18n: map[string]map[string]string{}}
	key := strings.Repeat("{a}", 1_000_000)
	got := pkg.Translate("en", key, map[string]string{"a": strings.Repeat("x", 100_000)})
	if len(got) > MaxTranslated {
		t.Fatalf("%d bytes", len(got))
	}
	if got := pkg.Translate("en", "Hi, {name}", map[string]string{"name": "Ann"}); got != "Hi, Ann" {
		t.Fatal(got)
	}
	// A value is not read again for placeholders: {a} → many {b}, b huge, used to
	// pass the size check and then ask for gigabytes.
	got = pkg.Translate("en", "{a}", map[string]string{"a": strings.Repeat("{b}", 20000), "b": strings.Repeat("x", 10<<20)})
	if got != strings.Repeat("{b}", 20000) {
		t.Fatalf("%d bytes", len(got))
	}
	if got := pkg.Translate("en", "{{name}} {x} {name", map[string]string{"name": "Ann"}); got != "{Ann} {x} {name" {
		t.Fatal(got)
	}
}
