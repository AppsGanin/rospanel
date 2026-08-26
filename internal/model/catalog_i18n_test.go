package model_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// The panel renders journal action names from its own dictionaries, keyed by the
// action. Nothing in Go can see that mapping, so an action added here and forgotten
// there shows the owner a bare "settings.changed" in the journal — and no compiler
// or type catches it, because the key is assembled at runtime.
//
// This test closes that gap from the Go side: every catalog key must have an entry
// in the reference dictionary. It reads ru.ts as text rather than parsing TypeScript
// — the keys are plain identifiers on their own lines, and a grep is both enough and
// immune to the file's formatting.
func dictKeys(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "web", "src", "i18n", "ru.ts")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("frontend dictionary not available (%v)", err)
	}
	return string(b)
}

// slug mirrors the frontend's slugKey: i18next reads a dot as nesting, so a dotted
// action is stored with underscores.
func slug(s string) string { return strings.ReplaceAll(s, ".", "_") }

func TestUserEventCatalogHasDictionaryEntries(t *testing.T) {
	dict := dictKeys(t)
	for _, key := range model.UserEventCatalog {
		if !strings.Contains(dict, slug(key)+":") {
			t.Errorf("event %q has no events.action.%s entry in web/src/i18n/ru.ts",
				key, slug(key))
		}
	}
}

func TestAdminAuditCatalogHasDictionaryEntries(t *testing.T) {
	dict := dictKeys(t)
	for _, e := range model.AdminAuditCatalog {
		if !strings.Contains(dict, slug(e.Key)+":") {
			t.Errorf("audit action %q has no audit.action.%s entry in web/src/i18n/ru.ts",
				e.Key, slug(e.Key))
		}
	}
	for _, c := range model.AdminAuditCategories {
		if !strings.Contains(dict, c+":") {
			t.Errorf("audit category %q has no audit.cat.%s entry in web/src/i18n/ru.ts", c, c)
		}
	}
}
