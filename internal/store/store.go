// Package store is the SQLite-backed persistence layer. The DB is the single
// source of truth; the Xray config is always derived from it.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// execer is the write half shared by *sql.DB and *sql.Tx. A statement written
// against it runs standalone or inside a transaction without its SQL living in two
// places — which is what lets a multi-row change be made atomic without forking
// every setter it touches.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// withTx runs fn inside a transaction, rolling back on any error. Worth reaching
// for whenever a change spans more than one row: the pool is a single connection
// (see Open), so a sequence of bare Exec calls is not just non-atomic, it is also
// slower — each one pays its own commit and fsync, where a transaction pays one.
func (s *Store) withTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ErrCorrupt reports a database file that exists but SQLite cannot use: a torn
// page, a truncated header ("file is not a database"), or a failed integrity
// check. It is the one failure a caller can act on — the file is unusable, so the
// only way back is a restore. Everything else from Open is a plain error.
var ErrCorrupt = errors.New("database is corrupt")

// Store wraps the SQLite connection pool.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, applies pragmas,
// verifies the file's integrity, and runs pending migrations. A file SQLite can't
// read, or one that fails the integrity check, comes back wrapped in ErrCorrupt.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single connection keeps writes serialized — correct and plenty for the
	// panel's scale (a handful of admins, hundreds of users).
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, corruptOr("open db", err)
	}
	if err := quickCheck(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, corruptOr("migrate", err)
	}
	return s, nil
}

// Check reports whether the database file at path is usable, without opening the
// panel's full Store (no migrations, no writes). A missing file is fine — that's a
// fresh install — so it returns nil. A damaged one returns ErrCorrupt.
//
// This runs at boot before anything touches the data dir, because the alternative
// is a panel that crash-loops on a corrupt file with no idea it should restore.
func Check(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil // fresh install: Open will create it
		}
		return err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(2000)")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return corruptOr("open db", err)
	}
	return quickCheck(db)
}

// quickCheck runs SQLite's quick_check pragma — the cheap sibling of
// integrity_check: it verifies page structure and skips the (slow) index-content
// cross-check, which is the right trade for a boot-time gate. A healthy database
// answers with the single row "ok".
func quickCheck(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA quick_check(1)`)
	if err != nil {
		return corruptOr("integrity check", err)
	}
	defer rows.Close()
	var result string
	if rows.Next() {
		if err := rows.Scan(&result); err != nil {
			return corruptOr("integrity check", err)
		}
	}
	if err := rows.Err(); err != nil {
		return corruptOr("integrity check", err)
	}
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return fmt.Errorf("%w: %s", ErrCorrupt, result)
	}
	return nil
}

// corruptOr tags an error as ErrCorrupt when SQLite is telling us the file itself
// is damaged, and leaves every other failure (locked, permissions, disk full) as a
// plain error — those must NOT trigger a restore.
func corruptOr(stage string, err error) error {
	msg := strings.ToLower(err.Error())
	for _, sig := range []string{
		"file is not a database",
		"database disk image is malformed",
		"malformed database schema",
		"database corrupt",
		"file is encrypted",
	} {
		if strings.Contains(msg, sig) {
			return fmt.Errorf("%w: %s: %v", ErrCorrupt, stage, err)
		}
	}
	return fmt.Errorf("%s: %w", stage, err)
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// InspectDB opens the database file at path read-only and reports whether it's a
// usable rospanel database, along with its user/admin counts and configured
// secret path. A non-nil error means the file isn't a valid panel DB (missing,
// corrupt, or — the classic empty-backup case — a header with no tables because
// the data was left in an unbacked-up WAL).
func InspectDB(path string) (users, admins int, secret string, err error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return 0, 0, "", err
	}
	defer db.Close()
	if err = db.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil {
		return 0, 0, "", fmt.Errorf("not a valid panel database: %w", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM admins`).Scan(&admins); err != nil {
		return 0, 0, "", fmt.Errorf("not a valid panel database: %w", err)
	}
	_ = db.QueryRow(`SELECT panel_secret_path FROM settings WHERE id = 1`).Scan(&secret)
	return users, admins, secret, nil
}

// Checkpoint flushes the write-ahead log into the main database file and
// truncates it, so an on-disk copy (e.g. a backup) captures all committed data.
// Without this a live backup would copy a near-empty .db with everything still in
// the uncheckpointed .db-wal (which backups intentionally exclude).
func (s *Store) Checkpoint() error {
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// boolToInt maps a Go bool to SQLite's 0/1 integer representation.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
	); err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, name,
		).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version) VALUES (?)`, name,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
