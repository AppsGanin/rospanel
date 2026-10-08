package pdb

import (
	"errors"
	"testing"
)

func TestCheckSQL(t *testing.T) {
	allowed := []string{
		`SELECT 1`,
		`CREATE TABLE t(a INTEGER PRIMARY KEY, b TEXT); INSERT INTO t(b) VALUES ('pragma; attach')`,
		`SELECT 'it''s; PRAGMA x' AS "PRAGMA;", [vacuum;] FROM t -- ; PRAGMA in a comment`,
		`SELECT 1 /* ; PRAGMA max_page_count = 1 */`,
		"SELECT `begin;` FROM t",
		`WITH x AS (SELECT 1) SELECT * FROM x`,
		`SELECT * FROM pragma_table_info('t')`, // read-only table-valued pragma
		`CREATE TRIGGER tr AFTER INSERT ON t BEGIN
			UPDATE t SET b = CASE WHEN new.b IS NULL THEN 'x' ELSE new.b END WHERE a = new.a;
			INSERT INTO log VALUES (new.a);
		END; SELECT 1`,
		`CREATE TEMP TRIGGER IF NOT EXISTS tr AFTER DELETE ON t BEGIN DELETE FROM u; END`,
		`SELECT CASE 1 WHEN 1 THEN 'end' END; SELECT 2`,
		`   ;;  SELECT 1;  `,
		`SELECT * FROM t WHERE a = :id AND b = $b AND c = @c AND d = ?1 AND e = ?`,
		`SELECT $a(x) FROM t`,
		``,
	}
	for _, q := range allowed {
		if err := CheckSQL(q); err != nil {
			t.Errorf("refused %q: %v", q, err)
		}
	}
	refused := []string{
		`PRAGMA max_page_count = 1000000`,
		`pragma/**/max_page_count = 1`,
		`SELECT 1; PRAGMA journal_mode = OFF`,
		`ATTACH '/var/lib/rospanel/rospanel.db' AS p`,
		`DETACH p`,
		`VACUUM INTO '/etc/cron.d/x'`,
		`BEGIN; DELETE FROM t; COMMIT`,
		`SAVEPOINT a`,
		`EXPLAIN PRAGMA max_page_count = 5`,
		`CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1; END; PRAGMA x = 1`,
		// Keywords as names must not open a "trigger body" the guard skips.
		`CREATE TABLE trigger(begin INT); PRAGMA max_page_count = 1000000`,
		`CREATE VIEW trigger AS SELECT 1 AS begin; PRAGMA max_page_count = 1000000`,
		`CREATE TRIGGER t AFTER INSERT ON begin BEGIN SELECT 1; END; PRAGMA max_page_count = 1000000`,
		`CREATE TEMP TRIGGER t AFTER INSERT ON x BEGIN SELECT CASE 1 WHEN 1 THEN 2 END; END; PRAGMA x = 1`,
		`SELECT CASE 1 WHEN 1 THEN 2 END; END`,
		"-- comment\nPRAGMA x",
		// SQLite reads $a(...) as one parameter, to the ")", quotes and all.
		`SELECT $a(');PRAGMA/**/max_page_count=99999;--'`,
		`SELECT @a(');PRAGMA/**/max_page_count=99999;--'`,
		`SELECT :a(');PRAGMA/**/max_page_count=99999;--'`,
		`SELECT #a(');PRAGMA/**/max_page_count=99999;--'`,
		`SELECT $a::b(");PRAGMA/**/max_page_count=99999;--"`,
	}
	for _, q := range refused {
		if err := CheckSQL(q); !errors.Is(err, ErrForbidden) {
			t.Errorf("allowed %q (err %v)", q, err)
		}
	}
	for _, q := range []string{`SELECT 'open`, `SELECT "open`, `SELECT [open`} {
		if err := CheckSQL(q); err == nil {
			t.Errorf("unterminated quote passed: %q", q)
		}
	}
}

func FuzzCheckSQL(f *testing.F) {
	for _, s := range []string{`SELECT 1`, `PRAGMA x`, `CREATE TRIGGER a BEGIN SELECT 1; END`, `'`, `/*`, `[`, "`"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, q string) {
		_ = CheckSQL(q) // must not panic or hang
	})
}
