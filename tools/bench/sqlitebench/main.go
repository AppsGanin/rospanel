// Command sqlitebench measures the write paths a panel on SQLite depends on: the
// filesystem's fsync rate, an autocommit insert (one commit and one fsync per row),
// the same rows in one transaction, and what synchronous=NORMAL would buy.
//
// Run it on the machine whose ceiling you want to know. The numbers belong to that
// disk and that kernel; comparing them across hosts is meaningless. The panel's own
// store opens with WAL, synchronous=FULL (unset, which is FULL) and a single
// connection, which is what this tool reproduces.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	n := flag.Int("n", 300, "writes per scenario")
	dir := flag.String("dir", os.TempDir(), "directory for the scratch database")
	payloadSize := flag.Int("payload", 256, "bytes per row")
	flag.Parse()
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	payload := make([]byte, *payloadSize)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}

	fsyncRate := measureFSync(*dir, *n)
	fmt.Printf("fsync            %8.0f /sec  (%.2f ms each)\n", fsyncRate, 1000/fsyncRate)

	full, batched, normal := measureSQLite(*dir, *n, string(payload))
	fmt.Printf("sqlite FULL      %8.0f writes/sec  (autocommit, one fsync per row)\n", full)
	fmt.Printf("sqlite FULL tx   %8.0f rows/sec    (%d rows in one transaction)\n", batched, *n)
	fmt.Printf("sqlite NORMAL    %8.0f writes/sec  (autocommit, loses durability on power loss)\n", normal)
}

func measureFSync(dir string, n int) float64 {
	path := filepath.Join(dir, "fsync.bin")
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.Remove(path)
	defer f.Close()
	buf := make([]byte, 4096)
	start := time.Now()
	for i := 0; i < n; i++ {
		if _, err := f.WriteAt(buf, 0); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := f.Sync(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	return float64(n) / time.Since(start).Seconds()
}

func measureSQLite(dir string, n int, payload string) (full, batched, normal float64) {
	path := filepath.Join(dir, "bench.db")
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		os.Remove(p)
	}
	defer func() {
		for _, p := range []string{path, path + "-wal", path + "-shm"} {
			os.Remove(p)
		}
	}()
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE w(id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	full = rate(n, func() error {
		_, err := db.Exec(`INSERT INTO w(v) VALUES(?)`, payload)
		return err
	})
	batchStart := time.Now()
	tx, err := db.Begin()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for i := 0; i < n; i++ {
		if _, err := tx.Exec(`INSERT INTO w(v) VALUES(?)`, payload); err != nil {
			_ = tx.Rollback()
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := tx.Commit(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	batched = float64(n) / time.Since(batchStart).Seconds()
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	normal = rate(n, func() error {
		_, err := db.Exec(`INSERT INTO w(v) VALUES(?)`, payload)
		return err
	})
	return full, batched, normal
}

func rate(n int, fn func() error) float64 {
	start := time.Now()
	for i := 0; i < n; i++ {
		if err := fn(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	return float64(n) / time.Since(start).Seconds()
}
