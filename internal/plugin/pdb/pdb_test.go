package pdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T, quota int64) *DB {
	t.Helper()
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "p.db"), quota)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestExecQueryValues(t *testing.T) {
	ctx := context.Background()
	d := open(t, 0)
	if _, err := d.Exec(ctx, `CREATE TABLE t(a INTEGER PRIMARY KEY, n INTEGER, s TEXT, f REAL, b BLOB)`, nil); err != nil {
		t.Fatal(err)
	}
	r, err := d.Exec(ctx, `INSERT INTO t(n, s, f, b) VALUES (?, ?, ?, ?)`,
		[]any{float64(5), "x", 1.5, map[string]any{"base64": "AAE="}})
	if err != nil || r.Changes != 1 || r.LastInsertID != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO t(n, s) VALUES (?, ?)`, []any{nil, float64(7)}); err != nil {
		t.Fatal(err)
	}
	rows, err := d.Query(ctx, `SELECT n, s, f, b FROM t ORDER BY a`, nil)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %v", rows, err)
	}
	if rows[0]["n"] != int64(5) || rows[0]["s"] != "x" || rows[0]["f"] != 1.5 {
		t.Fatalf("row 0: %#v", rows[0])
	}
	if b, _ := rows[0]["b"].(map[string]any); b["base64"] != "AAE=" {
		t.Fatalf("blob: %#v", rows[0]["b"])
	}
	// A whole JSON number into a TEXT column reads back as "7", not "7.0".
	if rows[1]["s"] != "7" || rows[1]["n"] != nil {
		t.Fatalf("row 1: %#v", rows[1])
	}
	if _, err := d.Query(ctx, `SELECT ?`, []any{[]int{1}}); err == nil {
		t.Fatal("an array argument must be refused")
	}
}

// The escape hatches the spike found, through the plugin-facing calls.
func TestPluginCannotEscapeItsFile(t *testing.T) {
	ctx := context.Background()
	d := open(t, 1<<20)
	other := filepath.Join(t.TempDir(), "other.db")
	for _, q := range []string{
		`ATTACH '` + other + `' AS o`,
		`VACUUM INTO '` + other + `'`,
		`PRAGMA max_page_count = 1000000`,
		`SELECT 1; VACUUM INTO '` + other + `'`,
	} {
		if _, err := d.Exec(ctx, q, nil); err == nil {
			t.Errorf("ran: %s", q)
		}
	}
	if _, err := os.Stat(other); err == nil {
		t.Fatal("a file was written outside the plugin's database")
	}
	if _, err := d.Query(ctx, `SELECT load_extension('/tmp/x.so')`, nil); err == nil {
		t.Fatal("load_extension ran")
	}
	// The pragma path is closed even below the guard: the attach limit holds.
	if _, err := d.conn.ExecContext(ctx, `ATTACH '`+other+`' AS o`); err == nil {
		t.Fatal("the connection accepts ATTACH")
	}
}

func TestQuota(t *testing.T) {
	ctx := context.Background()
	d := open(t, 256<<10)
	if _, err := d.Exec(ctx, `CREATE TABLE t(b BLOB)`, nil); err != nil {
		t.Fatal(err)
	}
	_, err := d.Exec(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 10000)
		INSERT INTO t SELECT randomblob(256) FROM n`, nil)
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("want ErrQuota, got %v", err)
	}
	if err := d.KVSet(ctx, "k", strings.Repeat("x", 60<<10)); err != nil && !errors.Is(err, ErrQuota) {
		t.Fatalf("got %v", err)
	}
}

func TestQueryBounds(t *testing.T) {
	ctx := context.Background()
	d := open(t, 0)
	_, err := d.Query(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 20000) SELECT i FROM n`, nil)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("rows: %v", err)
	}
	_, err = d.Query(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 100) SELECT hex(randomblob(30000)) FROM n`, nil)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("bytes: %v", err)
	}
}

func TestRunawayQueryHonoursContext(t *testing.T) {
	d := open(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 100_000_000) // 100ms
	defer cancel()
	_, err := d.Query(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n) SELECT count(*) FROM n`, nil)
	if err == nil {
		t.Fatal("an endless query returned")
	}
	if _, err := d.Query(context.Background(), `SELECT 1`, nil); err != nil {
		t.Fatalf("connection unusable after interrupt: %v", err)
	}
}

func TestTransactions(t *testing.T) {
	ctx := context.Background()
	d := open(t, 0)
	mustExec := func(q string) {
		t.Helper()
		if _, err := d.Exec(ctx, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int64 {
		t.Helper()
		rows, err := d.Query(ctx, `SELECT count(*) AS c FROM t`, nil)
		if err != nil {
			t.Fatal(err)
		}
		return rows[0]["c"].(int64)
	}
	mustExec(`CREATE TABLE t(a)`)
	if err := d.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Begin(ctx); err == nil {
		t.Fatal("nested Begin")
	}
	mustExec(`INSERT INTO t VALUES (1)`)
	if err := d.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("rollback kept the row")
	}
	_ = d.Begin(ctx)
	mustExec(`INSERT INTO t VALUES (1)`)
	if err := d.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// A transaction the call left open is rolled back by EndCall.
	_ = d.Begin(ctx)
	mustExec(`INSERT INTO t VALUES (2)`)
	d.EndCall()
	if count() != 1 {
		t.Fatalf("EndCall left %d rows", count())
	}
	if err := d.Commit(ctx); err == nil {
		t.Fatal("Commit without a transaction")
	}
}

func TestKV(t *testing.T) {
	ctx := context.Background()
	d := open(t, 0)
	if _, ok, err := d.KVGet(ctx, "a"); ok || err != nil {
		t.Fatal(ok, err)
	}
	for _, k := range []string{"a", "b%1", "b%2", "b_3", "c"} {
		if err := d.KVSet(ctx, k, "v-"+k); err != nil {
			t.Fatal(err)
		}
	}
	_ = d.KVSet(ctx, "a", "new")
	if v, ok, _ := d.KVGet(ctx, "a"); !ok || v != "new" {
		t.Fatal(v, ok)
	}
	// % and _ in a prefix are literal.
	list, err := d.KVList(ctx, "b%", "", 0)
	if err != nil || len(list) != 2 || list[0].Key != "b%1" || list[1].Key != "b%2" {
		t.Fatalf("%v %v", list, err)
	}
	page, _ := d.KVList(ctx, "", "b%2", 2)
	if len(page) != 2 || page[0].Key != "b_3" || page[1].Key != "c" {
		t.Fatalf("paging: %v", page)
	}
	_ = d.KVDelete(ctx, "a")
	if _, ok, _ := d.KVGet(ctx, "a"); ok {
		t.Fatal("deleted key still there")
	}
	if err := d.KVSet(ctx, "big", strings.Repeat("x", MaxKVValue+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	if err := d.KVSet(ctx, "", "x"); err == nil {
		t.Fatal("empty key")
	}
}

func TestMigrate(t *testing.T) {
	ctx := context.Background()
	d := open(t, 0)
	v1 := []Migration{
		{"0002_more.sql", `ALTER TABLE t ADD COLUMN b TEXT`},
		{"0001_init.sql", `CREATE TABLE t(a INTEGER PRIMARY KEY); CREATE INDEX t_a ON t(a)`},
	}
	applied, err := d.Migrate(ctx, v1)
	if err != nil || strings.Join(applied, ",") != "0001_init.sql,0002_more.sql" {
		t.Fatalf("%v %v", applied, err)
	}
	if applied, err := d.Migrate(ctx, v1); err != nil || len(applied) != 0 {
		t.Fatalf("re-run: %v %v", applied, err)
	}
	// A broken new migration lands nothing — not even the good one before it.
	v2 := append(v1, Migration{"0003_ok.sql", `CREATE TABLE u(x)`}, Migration{"0004_bad.sql", `CREATE TABLE t(dup)`})
	if _, err := d.Migrate(ctx, v2); err == nil {
		t.Fatal("bad migration applied")
	}
	if _, err := d.Query(ctx, `SELECT * FROM u`, nil); err == nil {
		t.Fatal("0003 landed without 0004")
	}
	changed := []Migration{{"0001_init.sql", `CREATE TABLE t(a)`}}
	if _, err := d.Migrate(ctx, changed); !errors.Is(err, ErrMigrationChanged) {
		t.Fatalf("got %v", err)
	}
	if _, err := d.Migrate(ctx, []Migration{{"0005.sql", `PRAGMA user_version = 3`}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("pragma migration: %v", err)
	}
}

func TestSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "p.db")
	d, err := Open(ctx, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = d.Exec(ctx, `CREATE TABLE t(a)`, nil)
	_, _ = d.Exec(ctx, `INSERT INTO t VALUES (1)`, nil)
	if err := Snapshot(ctx, path, path+".prev"); err != nil {
		t.Fatal(err)
	}
	_, _ = d.Exec(ctx, `INSERT INTO t VALUES (2)`, nil)
	_ = d.Close()
	if err := Restore(path+".prev", path); err != nil {
		t.Fatal(err)
	}
	d, err = Open(ctx, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.Query(ctx, `SELECT count(*) AS c FROM t`, nil)
	if err != nil || rows[0]["c"] != int64(1) {
		t.Fatalf("restored %v %v", rows, err)
	}
	if err := d.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if d.Size() == 0 {
		t.Fatal("size")
	}
}

// One value is held to MaxValue: SQLite refuses to build a bigger one, so a query
// cannot allocate hundreds of megabytes in the panel's process.
func TestValueLengthLimit(t *testing.T) {
	d := open(t, 0)
	ctx := context.Background()
	if _, err := d.Query(ctx, "SELECT length(randomblob(?)) AS n", []any{float64(MaxValue + 1)}); err == nil {
		t.Fatal("a value over the limit was built")
	}
	rows, err := d.Query(ctx, "SELECT length(randomblob(1000)) AS n", nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
}

// A TEMP table is held to the quota too: it lives outside the main file.
func TestTempTablesWithinQuota(t *testing.T) {
	d := open(t, 1<<20)
	ctx := context.Background()
	if _, err := d.Exec(ctx, "CREATE TEMP TABLE big(b BLOB)", nil); err != nil {
		t.Fatal(err)
	}
	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, err = d.Exec(ctx, "INSERT INTO big VALUES (randomblob(200000))", nil)
	}
	if err == nil {
		t.Fatal("10 MB went into a TEMP table under a 1 MB quota")
	}
}

// Full is judged by the pages in use: a plugin that hit its quota and then cleaned
// up is not full any more, though SQLite keeps the file at its size.
func TestOverQuotaByPagesInUse(t *testing.T) {
	d := open(t, 1<<20)
	ctx := context.Background()
	if _, err := d.Exec(ctx, "CREATE TABLE big(b BLOB)", nil); err != nil {
		t.Fatal(err)
	}
	if d.OverQuota() {
		t.Fatal("empty and over quota")
	}
	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, err = d.Exec(ctx, "INSERT INTO big VALUES (randomblob(100000))", nil)
	}
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("filled without hitting the quota: %v", err)
	}
	if !d.OverQuota() {
		t.Fatal("full and not over quota")
	}
	if _, err := d.Exec(ctx, "DELETE FROM big", nil); err != nil {
		t.Fatal(err)
	}
	if d.OverQuota() {
		t.Fatal("still over quota after deleting everything")
	}
}
