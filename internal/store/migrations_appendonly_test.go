package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The migration ledger records the FILENAME only, with no checksum: store.migrate skips
// any version already in schema_migrations. That makes the migrations directory
// append-only in practice, and breaking that rule fails in two silent, opposite ways —
//
//   - EDIT a shipped file: it never re-runs, so an upgraded box quietly lacks the change
//     while a fresh install has it. Two schemas, no error on either.
//   - RENAME a shipped file (even keeping its number): it looks new, so it re-runs on
//     every existing box — an ADD COLUMN then fails with "duplicate column name" and
//     takes the panel down at boot, while CI, which only ever builds fresh databases,
//     stays green.
//
// This test is the guard the runner can't be: it pins the name and content hash of every
// migration. A failure here is either a real mistake, or a deliberate NEW migration that
// should be added to the golden file (never an edit to an existing line).
func TestMigrationsAreAppendOnly(t *testing.T) {
	const golden = "testdata/migrations.sha256"

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var lines []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		sum := sha256.Sum256(body)
		lines = append(lines, e.Name()+"  "+hex.EncodeToString(sum[:]))
	}
	sort.Strings(lines)
	got := strings.Join(lines, "\n") + "\n"

	want, err := os.ReadFile(golden)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("seed golden: %v", err)
		}
		t.Skipf("seeded %s — commit it", golden)
	}
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(want) == got {
		return
	}

	oldSet := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(want)), "\n") {
		if name, sum, ok := strings.Cut(l, "  "); ok {
			oldSet[name] = sum
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		name, sum, ok := strings.Cut(l, "  ")
		if !ok {
			continue
		}
		if prev, existed := oldSet[name]; existed && prev != sum {
			t.Errorf("%s was EDITED after shipping: it will not re-run on any box that "+
				"already applied it, so upgraded and fresh installs end up with different "+
				"schemas. Add a NEW migration instead.", name)
		}
		delete(oldSet, name)
	}
	for name := range oldSet {
		t.Errorf("%s was REMOVED or RENAMED: a rename re-runs on every existing box and "+
			"fails at boot (duplicate column), while CI's fresh databases stay green.", name)
	}
	t.Logf("if this is a genuinely new migration, update %s (append only)", golden)
}
