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
}
