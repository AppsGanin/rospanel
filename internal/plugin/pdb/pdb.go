// Package pdb is a plugin's own SQLite database: one file per plugin, beside the
// panel's but never inside it.
//
// The panel's database is off limits for three reasons that are all about the panel
// rather than the plugin: SQLite has one writer per file (a plugin's long write
// would stall payments and stats), reads compete with the WAL checkpoint (edits
// once went from 76 ms to 4.5 s that way), and its migrations are append-only and
// its schema internal. A file of its own has its own writer, its own WAL and its own
// migration history, and costs the panel nothing when a plugin misbehaves.
//
// What holds a plugin to its file is set on the one connection it gets: no attached
// databases (which also stops VACUUM INTO writing a copy anywhere), a page quota,
// and CheckSQL refusing the statements that could undo either. All verified against
// modernc sqlite on 2026-10-06.
package pdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	// MaxRows and MaxResultBytes bound one query's answer: it is handed to a VM
	// whose heap is tens of megabytes.
	MaxRows        = 10000
	MaxResultBytes = 4 << 20
	// MaxValue bounds one string or blob value in the plugin's database (and so a
	// row being built), MaxSQL one statement's text.
	MaxValue = 8 << 20
	MaxSQL   = 1 << 20
	// MaxKVValue bounds one kv value.
	MaxKVValue = 64 << 10
	pageSize   = 4096
)

var (
	// ErrQuota is returned once the file has outgrown its quota.
	ErrQuota = errors.New("pdb: database is over its quota")
	// ErrTooLarge is returned for a result or value past its bound.
	ErrTooLarge = errors.New("pdb: result too large")
	// ErrMigrationChanged is returned when an applied migration's text changed.
	ErrMigrationChanged = errors.New("pdb: an applied migration was changed")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("pdb: closed")
)

// DB is one plugin's database. Calls are serialized: a plugin's calls are anyway.
type DB struct {
	path  string
	quota int64

	mu   sync.Mutex
	db   *sql.DB
	conn *sql.Conn
	tx   bool // a plugin transaction is open on conn
	// hitQuota: a write since the last OverQuota was refused for the quota.
	hitQuota bool
}

// Open opens (creating if needed) the database at path with a quota in bytes.
func Open(ctx context.Context, path string, quota int64) (*DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"+
		// A checkpoint shrinks the WAL back to this: one big write must not leave
		// the file at its peak for good.
		"&_pragma=journal_size_limit(8388608)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	d := &DB{path: path, quota: quota, db: db, conn: conn}
	if err := d.lockDown(ctx); err != nil {
		_ = d.Close()
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS _kv (
		key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL) WITHOUT ROWID;
		CREATE TABLE IF NOT EXISTS _migrations (
		name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at INTEGER NOT NULL) WITHOUT ROWID`); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// lockDown sets what the plugin must not be able to change: no attachments, and
// the quota as a page ceiling. The connection is held for the DB's life, so these
// stay set — CheckSQL keeps the plugin from issuing the pragma that would lift them.
func (d *DB) lockDown(ctx context.Context) error {
	if _, err := sqlite.Limit(d.conn, sqlite3.SQLITE_LIMIT_ATTACHED, 0); err != nil {
		return fmt.Errorf("pdb: attach limit: %w", err)
	}
	// One value (a string, a blob, a row being built) stays small. Without it
	// SELECT randomblob(800000000) allocated 800 MB in the panel's own process —
	// memory the plugin's wasm cap does not see.
	if _, err := sqlite.Limit(d.conn, sqlite3.SQLITE_LIMIT_LENGTH, MaxValue); err != nil {
		return fmt.Errorf("pdb: length limit: %w", err)
	}
	if _, err := sqlite.Limit(d.conn, sqlite3.SQLITE_LIMIT_SQL_LENGTH, MaxSQL); err != nil {
		return fmt.Errorf("pdb: sql limit: %w", err)
	}
	if d.quota > 0 {
		if err := d.applyQuota(ctx); err != nil {
			return fmt.Errorf("pdb: quota: %w", err)
		}
	}
	return nil
}

// applyQuota sets the page ceiling on the main database and on the temporary one: a
// TEMP table lives in a file of its own, which the main ceiling does not cover.
func (d *DB) applyQuota(ctx context.Context) error {
	pages := (d.quota + pageSize - 1) / pageSize
	for _, schema := range []string{"main", "temp"} {
		if _, err := d.conn.ExecContext(ctx, fmt.Sprintf("PRAGMA %s.max_page_count = %d", schema, pages)); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database, rolling back an open transaction.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return nil
	}
	if d.tx {
		_, _ = d.conn.ExecContext(context.Background(), "ROLLBACK")
		d.tx = false
	}
	err := errors.Join(d.conn.Close(), d.db.Close())
	d.db, d.conn = nil, nil
	return err
}

// Path is the database file.
func (d *DB) Path() string { return d.path }

// Result is what Exec reports.
type Result struct {
	Changes      int64 `json:"changes"`
	LastInsertID int64 `json:"last_insert_id"`
}

// Exec runs plugin SQL that returns no rows.
func (d *DB) Exec(ctx context.Context, query string, args []any) (Result, error) {
	if err := CheckSQL(query); err != nil {
		return Result{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return Result{}, ErrClosed
	}
	vals, err := bindArgs(args)
	if err != nil {
		return Result{}, err
	}
	r, err := d.conn.ExecContext(ctx, query, vals...)
	if err != nil {
		return Result{}, d.mapErr(err)
	}
	var res Result
	res.Changes, _ = r.RowsAffected()
	res.LastInsertID, _ = r.LastInsertId()
	return res, nil
}

// Query runs plugin SQL and returns its rows as objects keyed by column name.
// Values come back as JSON-ready types: int64, float64, string, nil, and
// {"base64": "…"} for blobs.
func (d *DB) Query(ctx context.Context, query string, args []any) ([]map[string]any, error) {
	if err := CheckSQL(query); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return nil, ErrClosed
	}
	vals, err := bindArgs(args)
	if err != nil {
		return nil, err
	}
	rows, err := d.conn.QueryContext(ctx, query, vals...)
	if err != nil {
		return nil, d.mapErr(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	size := 0
	for rows.Next() {
		if len(out) == MaxRows {
			return nil, fmt.Errorf("%w: more than %d rows — page with LIMIT", ErrTooLarge, MaxRows)
		}
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			v := resultValue(cells[i])
			size += len(c) + valueSize(v)
			row[c] = v
		}
		if size > MaxResultBytes {
			return nil, fmt.Errorf("%w: over %d bytes — page with LIMIT", ErrTooLarge, MaxResultBytes)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, d.mapErr(err)
	}
	return out, nil
}

// Begin opens the plugin's transaction. One at a time; EndCall rolls back one the
// call left open.
func (d *DB) Begin(ctx context.Context) error { return d.txStmt(ctx, "BEGIN IMMEDIATE", true) }

// Commit commits the plugin's transaction.
func (d *DB) Commit(ctx context.Context) error { return d.txStmt(ctx, "COMMIT", false) }

// Rollback rolls the plugin's transaction back.
func (d *DB) Rollback(ctx context.Context) error { return d.txStmt(ctx, "ROLLBACK", false) }

func (d *DB) txStmt(ctx context.Context, stmt string, open bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return ErrClosed
	}
	if d.tx == open {
		if open {
			return errors.New("pdb: a transaction is already open")
		}
		return errors.New("pdb: no transaction is open")
	}
	if _, err := d.conn.ExecContext(ctx, stmt); err != nil {
		if !open {
			// A failed COMMIT leaves SQLite's transaction open; don't strand it.
			_, _ = d.conn.ExecContext(context.Background(), "ROLLBACK")
			d.tx = false
		}
		return d.mapErr(err)
	}
	d.tx = open
	return nil
}

// EndCall closes what a plugin call left behind: an open transaction is rolled
// back. The host calls it after every call into the plugin.
func (d *DB) EndCall() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil && d.tx {
		_, _ = d.conn.ExecContext(context.Background(), "ROLLBACK")
		d.tx = false
	}
	// The quota again, whatever the call did: CheckSQL keeps a plugin from the
	// pragma, and this keeps a gap in CheckSQL from lasting past one call.
	if d.conn != nil && d.quota > 0 {
		_ = d.applyQuota(context.Background())
	}
}

// Size is the bytes the database occupies on disk, its WAL included.
func (d *DB) Size() int64 {
	var n int64
	for _, suffix := range []string{"", "-wal"} {
		if st, err := os.Stat(d.path + suffix); err == nil {
			n += st.Size()
		}
	}
	return n
}

// OverQuota reports whether the database filled up since it was last asked: a
// write was refused for the quota and the pages in use are still near the ceiling
// (a plugin that made room in the same call is fine; free pages left by deletes
// are written again and do not count). A WAL that ran away past the quota — the
// page ceiling does not bound it — is over too.
func (d *DB) OverQuota() bool {
	if d.quota <= 0 {
		return false
	}
	if st, err := os.Stat(d.path + "-wal"); err == nil && st.Size() > d.quota+64<<20 {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	hit := d.hitQuota
	d.hitQuota = false
	if !hit || d.conn == nil {
		return false
	}
	var pages, free int64
	ctx := context.Background()
	if d.conn.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages) != nil ||
		d.conn.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free) != nil {
		return false
	}
	return pages-free >= (d.quota+pageSize-1)/pageSize*9/10
}

// Checkpoint folds the WAL into the main file — before a backup copies the file,
// which skips -wal files.
func (d *DB) Checkpoint(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return ErrClosed
	}
	if d.tx {
		return errors.New("pdb: a transaction is open")
	}
	_, err := d.conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// KVGet returns a kv value, or ok=false.
func (d *DB) KVGet(ctx context.Context, key string) (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return "", false, ErrClosed
	}
	var v string
	err := d.conn.QueryRowContext(ctx, `SELECT value FROM _kv WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, d.mapErr(err)
}

// KVSet stores a kv value.
func (d *DB) KVSet(ctx context.Context, key, value string) error {
	if len(value) > MaxKVValue {
		return fmt.Errorf("%w: kv value over %d bytes", ErrTooLarge, MaxKVValue)
	}
	if key == "" || len(key) > 512 {
		return errors.New("pdb: kv key must be 1-512 bytes")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return ErrClosed
	}
	_, err := d.conn.ExecContext(ctx, `INSERT INTO _kv(key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().Unix())
	return d.mapErr(err)
}

// KVDelete removes a kv value; a missing key is not an error.
func (d *DB) KVDelete(ctx context.Context, key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return ErrClosed
	}
	_, err := d.conn.ExecContext(ctx, `DELETE FROM _kv WHERE key = ?`, key)
	return d.mapErr(err)
}

// KVEntry is one kv pair.
type KVEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// KVList returns up to limit pairs whose key starts with prefix, ordered by key,
// starting after the key `after` (for paging).
func (d *DB) KVList(ctx context.Context, prefix, after string, limit int) ([]KVEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return nil, ErrClosed
	}
	// A range scan rather than LIKE: a prefix may contain % or _.
	rows, err := d.conn.QueryContext(ctx, `SELECT key, value FROM _kv
		WHERE key >= ? AND key > ? AND substr(key, 1, length(?)) = ? ORDER BY key LIMIT ?`,
		prefix, after, prefix, prefix, limit)
	if err != nil {
		return nil, d.mapErr(err)
	}
	defer rows.Close()
	out := []KVEntry{}
	size := 0
	for rows.Next() {
		var e KVEntry
		if err := rows.Scan(&e.Key, &e.Value); err != nil {
			return nil, err
		}
		// A page ends at MaxResultBytes too: a thousand 64 KB values would be 64 MB
		// for the plugin's heap and more as JSON. The caller pages on with after.
		if size += len(e.Key) + len(e.Value); size > MaxResultBytes && len(out) > 0 {
			break
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Migration is one file of a plugin's migrations/ directory.
type Migration struct {
	Name string // "0001_init.sql"
	SQL  string
}

// Migrate applies the migrations not applied yet, in name order, all in one
// transaction: either every new one lands or none does. An applied migration whose
// text changed is refused — the plugin's history, like the panel's, is append-only.
func (d *DB) Migrate(ctx context.Context, ms []Migration) (applied []string, err error) {
	sorted := append([]Migration(nil), ms...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, m := range sorted {
		if err := CheckSQL(m.SQL); err != nil {
			return nil, fmt.Errorf("migration %s: %w", m.Name, err)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return nil, ErrClosed
	}
	if d.tx {
		return nil, errors.New("pdb: a transaction is open")
	}
	done := map[string]string{}
	rows, err := d.conn.QueryContext(ctx, `SELECT name, sha256 FROM _migrations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n, h string
		if err := rows.Scan(&n, &h); err != nil {
			rows.Close()
			return nil, err
		}
		done[n] = h
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var todo []Migration
	for _, m := range sorted {
		sum := sha256.Sum256([]byte(m.SQL))
		h := hex.EncodeToString(sum[:])
		if old, ok := done[m.Name]; ok {
			if old != h {
				return nil, fmt.Errorf("%w: %s", ErrMigrationChanged, m.Name)
			}
			continue
		}
		todo = append(todo, m)
	}
	if len(todo) == 0 {
		return nil, nil
	}
	if _, err := d.conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, err
	}
	for _, m := range todo {
		sum := sha256.Sum256([]byte(m.SQL))
		if _, err := d.conn.ExecContext(ctx, m.SQL); err != nil {
			_, _ = d.conn.ExecContext(context.Background(), "ROLLBACK")
			return nil, fmt.Errorf("migration %s: %w", m.Name, d.mapErr(err))
		}
		if _, err := d.conn.ExecContext(ctx, `INSERT INTO _migrations(name, sha256, applied_at) VALUES (?, ?, ?)`,
			m.Name, hex.EncodeToString(sum[:]), time.Now().Unix()); err != nil {
			_, _ = d.conn.ExecContext(context.Background(), "ROLLBACK")
			return nil, err
		}
		applied = append(applied, m.Name)
	}
	if _, err := d.conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = d.conn.ExecContext(context.Background(), "ROLLBACK")
		return nil, err
	}
	return applied, nil
}

// Snapshot writes a consistent copy of the database at path to dst (replacing it),
// for rolling an update back. It opens its own connection: the plugin's cannot
// attach, which VACUUM INTO needs. A database not created yet snapshots as an
// empty file — SQLite reads that as an empty database, which is what a rollback
// should bring back. The copy is written aside and renamed into place, so a crash
// never leaves half a snapshot to be restored.
func Snapshot(ctx context.Context, path, dst string) error {
	_ = os.Remove(dst) // never leave an older snapshot standing in for this one
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Rename(tmp, dst)
	} else if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := syncFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func syncFile(name string) error {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Sweep removes what a crash in Snapshot or Restore left beside the databases in
// dir.
func Sweep(dir string) {
	for _, pattern := range []string{"*.prev.tmp", "*.restore"} {
		names, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, n := range names {
			_ = os.Remove(n)
		}
	}
}

// Restore puts a snapshot back in place of the database at path. The database
// must be closed.
// The snapshot is used up: it belongs to one update, and a later rollback must not
// find it standing in for its own.
func Restore(snapshot, path string) error {
	src, err := os.Open(snapshot)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := path + ".restore"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil { // streamed: a snapshot can be a gigabyte
		dst.Close()
		os.Remove(tmp)
		return err
	}
	if err := dst.Sync(); err != nil { // on disk before it replaces the database
		dst.Close()
		os.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return os.Remove(snapshot)
}

// Remove deletes the database files at path.
func Remove(path string) error {
	var errs []error
	for _, suffix := range []string{"", "-wal", "-shm", ".prev"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// bindArgs turns JSON-decoded arguments into SQLite values. Whole numbers bind as
// integers — JSON has only floats, and 5.0 into a TEXT column would read "5.0" —
// and {"base64": "…"} binds as a blob.
func bindArgs(args []any) ([]any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case nil, string, bool, int64:
			out[i] = v
		case float64:
			if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
				out[i] = int64(v)
			} else {
				out[i] = v
			}
		case map[string]any:
			s, ok := v["base64"].(string)
			if !ok || len(v) != 1 {
				return nil, fmt.Errorf("pdb: argument %d: objects must be {base64: \"…\"}", i+1)
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, fmt.Errorf("pdb: argument %d: %w", i+1, err)
			}
			out[i] = b
		default:
			return nil, fmt.Errorf("pdb: argument %d: unsupported %T", i+1, a)
		}
	}
	return out, nil
}

func resultValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return map[string]any{"base64": base64.StdEncoding.EncodeToString(x)}
	case time.Time: // the driver parses DATETIME-declared columns
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return x
	}
}

func valueSize(v any) int {
	switch x := v.(type) {
	case string:
		return len(x)
	case map[string]any:
		s, _ := x["base64"].(string)
		return len(s)
	default:
		return 8
	}
}

// mapErr names the quota error (SQLite says "database or disk is full") and
// remembers it for OverQuota.
func (d *DB) mapErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "database or disk is full") {
		d.hitQuota = true
		return fmt.Errorf("%w: %v", ErrQuota, err)
	}
	return err
}
