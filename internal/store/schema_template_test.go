package store

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// noSchemaTemplate makes Open replay the migrations for the rest of the test, the way
// it does in production, and restores the cache afterwards.
func noSchemaTemplate(t *testing.T) {
	t.Helper()
	schemaTemplate.mu.Lock()
	savedBytes, savedTried := schemaTemplate.bytes, schemaTemplate.tried
	schemaTemplate.bytes, schemaTemplate.tried = nil, true
	schemaTemplate.mu.Unlock()
	t.Cleanup(func() {
		schemaTemplate.mu.Lock()
		schemaTemplate.bytes, schemaTemplate.tried = savedBytes, savedTried
		schemaTemplate.mu.Unlock()
	})
}

// dumpDB renders a database as text: every object in sqlite_master, then every row of
// every table, with any column named "…_at" blanked. Those are clocks: two databases
// created a second apart differ there for reasons that say nothing about the schema,
// and whether they hold the RIGHT time is what TestSeededTimestampsAreRestamped is
// for.
func dumpDB(t *testing.T, s *Store) string {
	t.Helper()
	var b strings.Builder
	var tables []string
	rows, err := s.db.Query(`SELECT type, name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	for rows.Next() {
		var typ, name, sqlText string
		if err := rows.Scan(&typ, &name, &sqlText); err != nil {
			t.Fatalf("scan: %v", err)
		}
		fmt.Fprintf(&b, "%s %s\n%s\n", typ, name, sqlText)
		if typ == "table" {
			tables = append(tables, name)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}

	sort.Strings(tables)
	for _, tbl := range tables {
		r, err := s.db.Query(`SELECT * FROM "` + tbl + `" ORDER BY 1`)
		if err != nil {
			t.Fatalf("read %s: %v", tbl, err)
		}
		cols, _ := r.Columns()
		fmt.Fprintf(&b, "-- %s(%s)\n", tbl, strings.Join(cols, ","))
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := r.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: %v", tbl, err)
			}
			for i, c := range cols {
				if strings.HasSuffix(c, "_at") {
					vals[i] = "<time>"
				}
			}
			fmt.Fprintf(&b, "%v\n", vals)
		}
		_ = r.Close()
		if err := r.Err(); err != nil {
			t.Fatalf("read %s: %v", tbl, err)
		}
	}
	return b.String()
}

// The schema template is an optimization, and an optimization that changes what a new
// database contains is a bug that would show up as inexplicable test failures much
// later. A database seeded from the template must be indistinguishable from one built
// by replaying every migration — same objects, same seeded rows.
func TestSchemaTemplateMatchesAReplay(t *testing.T) {
	dir := t.TempDir()

	replayed, err := func() (*Store, error) {
		noSchemaTemplate(t)
		return Open(filepath.Join(dir, "replayed.db"))
	}()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	defer replayed.Close()

	// Warm the cache (this one replays too), then the third open is the copy.
	warm, err := Open(filepath.Join(dir, "warm.db"))
	if err != nil {
		t.Fatalf("warm: %v", err)
	}
	_ = warm.Close()
	seeded, err := Open(filepath.Join(dir, "seeded.db"))
	if err != nil {
		t.Fatalf("seeded: %v", err)
	}
	defer seeded.Close()

	if got, want := dumpDB(t, seeded), dumpDB(t, replayed); got != want {
		t.Errorf("a database seeded from the template differs from a replayed one:\n--- replayed\n%s\n--- seeded\n%s", want, got)
	}
}

// The copy carries the template's timestamps, so Open restamps them: a row should say
// when THIS database was made, which is what a replay would have written.
func TestSeededTimestampsAreRestamped(t *testing.T) {
	dir := t.TempDir()
	warm, err := Open(filepath.Join(dir, "warm.db"))
	if err != nil {
		t.Fatalf("warm: %v", err)
	}
	_ = warm.Close()

	st, err := Open(filepath.Join(dir, "seeded.db"))
	if err != nil {
		t.Fatalf("seeded: %v", err)
	}
	defer st.Close()

	for _, c := range []struct{ what, query string }{
		{"migration rows", `SELECT COUNT(*) FROM schema_migrations WHERE applied_at < unixepoch() - 60 OR applied_at = 0`},
		{"the settings row", `SELECT COUNT(*) FROM settings WHERE updated_at < unixepoch() - 60 OR updated_at = 0`},
	} {
		var stale int
		if err := st.db.QueryRow(c.query).Scan(&stale); err != nil {
			t.Fatalf("count %s: %v", c.what, err)
		}
		if stale != 0 {
			t.Errorf("%s kept the template's timestamp (%d)", c.what, stale)
		}
	}
}
