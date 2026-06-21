// Package store is the SQLite-backed persistence layer. The DB is the single
// source of truth; the Xray config is always derived from it.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the SQLite connection pool.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, applies pragmas,
// and runs pending migrations.
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
	// panel's scale (one admin, hundreds of users).
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
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
