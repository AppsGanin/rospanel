package store

import (
	"database/sql"
	"errors"
	"io/fs"
	"log"
	"os"
	"sync"
	"testing"
)

// Replaying every migration is what a brand-new database costs, and it is not a
// small cost under the race detector: modernc's SQLite is pure Go, so every parse of
// every CREATE/ALTER is instrumented. Measured on one machine: ~1.95s to open a fresh
// database against ~0.06s without -race, and ~0.02s to reopen one already migrated.
// A test that wants its own store pays the whole thing, so a package's test time
// tracks (tests × migrations) — internal/core reached 599.7s against Go's 10-minute
// per-package timeout, and the next commit tipped it over.
//
// So the replay happens once per process and the result is kept in memory; every
// later fresh database is written from that copy, leaving migrate() nothing to do.
// The copy is byte-identical to a replay except for schema_migrations.applied_at
// (verified by dumping two freshly migrated databases and diffing — the timestamps
// were the only difference), and Open restamps those anyway.
//
// Only test binaries ever open a second fresh database: the panel and the node open
// exactly one, and only the very first boot creates it. Hence the testing.Testing()
// gate — in production this would hold a few hundred KB to serve a copy nobody asks
// for, and the point of the cache is to be invisible outside the test suites.

var schemaTemplate struct {
	mu    sync.Mutex
	bytes []byte
	tried bool // a failed snapshot is not retried: it would fail the same way
}

// isFreshPath reports whether Open is about to create the database rather than open
// an existing one. Anything other than "not there" (a permissions error, say) counts
// as not fresh: the open itself is the right place for that failure to surface.
func isFreshPath(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// seedFromTemplate writes the cached freshly-migrated database to path, and reports
// whether it did. A failure to write is not fatal — the caller carries on and the
// migrations replay as they always did.
func seedFromTemplate(path string) bool {
	if !testing.Testing() {
		return false
	}
	schemaTemplate.mu.Lock()
	b := schemaTemplate.bytes
	schemaTemplate.mu.Unlock()
	if len(b) == 0 {
		return false
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		log.Printf("[WARN] store: could not seed %s from the schema template: %v", path, err)
		return false
	}
	return true
}

// restampSeeded rewrites the timestamps a copy inherited from the template so the
// database says when IT was made, which is what a replay would have written. The list
// is short because a fresh database has only two timestamped rows — the migration
// ledger and the settings row a migration seeds — and it is checked rather than
// trusted: TestSchemaTemplateMatchesAReplay compares a seeded database against a
// replayed one field by field, so a future migration that seeds another one fails
// there instead of quietly handing tests a stale clock.
func restampSeeded(db *sql.DB) error {
	for _, q := range []string{
		`UPDATE schema_migrations SET applied_at = unixepoch()`,
		`UPDATE settings SET updated_at = unixepoch()`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// keepSchemaTemplate snapshots a just-migrated database for the rest of the process.
// VACUUM INTO rather than reading the file: it writes one self-contained database
// with no WAL of its own, so the snapshot can't miss changes still sitting in the
// write-ahead log.
func keepSchemaTemplate(db *sql.DB) {
	if !testing.Testing() {
		return
	}
	schemaTemplate.mu.Lock()
	defer schemaTemplate.mu.Unlock()
	if schemaTemplate.tried {
		return
	}
	schemaTemplate.tried = true

	f, err := os.CreateTemp("", "rospanel-schema-*.db")
	if err != nil {
		log.Printf("[WARN] store: schema template: %v", err)
		return
	}
	tmp := f.Name()
	_ = f.Close()
	// VACUUM INTO refuses to overwrite, so the placeholder has to go first.
	if err := os.Remove(tmp); err != nil {
		log.Printf("[WARN] store: schema template: %v", err)
		return
	}
	defer func() { _ = os.Remove(tmp) }()

	if _, err := db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		log.Printf("[WARN] store: schema template: %v", err)
		return
	}
	b, err := os.ReadFile(tmp)
	if err != nil {
		log.Printf("[WARN] store: schema template: %v", err)
		return
	}
	schemaTemplate.bytes = b
}
