package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// deadColumns are the ones 0039 removes. Kept as a list so the assertion below reads
// as "none of these exist any more" rather than as thirteen separate checks.
var deadColumns = map[string][]string{
	"settings": {
		"trojan_enabled", "trojan_port", "trojan_fp", "trojan_name", "ws_path",
		"yookassa_enabled", "yookassa_shop_id", "yookassa_secret_key", "yookassa_test",
		"cryptobot_enabled", "cryptobot_token", "cryptobot_testnet",
	},
	"nodes": {"trojan_enabled"},
}

func columnExists(t *testing.T, st *Store, table, column string) bool {
	t.Helper()
	var n int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column,
	).Scan(&n); err != nil {
		t.Fatalf("pragma %s.%s: %v", table, column, err)
	}
	return n > 0
}

// The columns of the retired Trojan-WS lane and of the pre-registry payment settings
// are gone. They were deliberately left behind by earlier migrations (a rollback to
// the previous binary, and credentials an operator might not have re-entered); both
// reasons expired, and dead schema is how a stale column later gets read by accident.
func TestDeadColumnsAreGone(t *testing.T) {
	st := newStore(t)
	for table, cols := range deadColumns {
		for _, c := range cols {
			if columnExists(t, st, table, c) {
				t.Errorf("%s.%s still exists — did 0039 stop running?", table, c)
			}
		}
	}
}

// The drop must not take live credentials with it: an install that upgraded past the
// provider registry but never re-entered its keys has them ONLY in those columns, so
// 0039 moves them into payment_providers first. Simulated by building the database in
// its pre-0039 shape and letting the migration run over it.
func TestDeadColumnsRescueLegacyPaymentKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	// Open once to get the full schema, then rewind: delete 0039's marker and put the
	// columns back with values, exactly like a panel that upgraded before 0039 shipped.
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE settings ADD COLUMN yookassa_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN yookassa_shop_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN yookassa_secret_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN yookassa_test INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN cryptobot_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN cryptobot_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN cryptobot_testnet INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN trojan_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN trojan_port INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN trojan_fp TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN trojan_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN ws_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN trojan_enabled INTEGER`,
		// A shop secret with a quote in it: string concatenation in the migration
		// would have produced invalid JSON and lost the key.
		`UPDATE settings SET yookassa_enabled = 1, yookassa_shop_id = '123456',
		        yookassa_secret_key = 'live_a"b\c', yookassa_test = 1,
		        cryptobot_token = 'tok-123', cryptobot_testnet = 0 WHERE id = 1`,
		`DELETE FROM schema_migrations WHERE version = '0039_drop_dead_columns.sql'`,
	} {
		if _, err := st.db.Exec(stmt); err != nil {
			t.Fatalf("rewind (%s): %v", stmt, err)
		}
	}
	st.Close()

	// Re-open: 0039 runs again, this time over legacy data.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()

	for table, cols := range deadColumns {
		for _, c := range cols {
			if columnExists(t, st2, table, c) {
				t.Errorf("%s.%s survived the migration", table, c)
			}
		}
	}

	got := map[string]map[string]string{}
	rows, err := st2.db.Query(`SELECT key, enabled, config FROM payment_providers`)
	if err != nil {
		t.Fatalf("providers: %v", err)
	}
	defer rows.Close()
	enabled := map[string]int{}
	for rows.Next() {
		var k, cfg string
		var en int
		if err := rows.Scan(&k, &en, &cfg); err != nil {
			t.Fatalf("scan: %v", err)
		}
		m := map[string]string{}
		if err := json.Unmarshal([]byte(cfg), &m); err != nil {
			t.Fatalf("provider %s config is not valid JSON (%v): %s", k, err, cfg)
		}
		got[k], enabled[k] = m, en
	}
	if got["yookassa"]["secret_key"] != `live_a"b\c` || got["yookassa"]["shop_id"] != "123456" {
		t.Errorf("the YooKassa keys were lost: %+v", got["yookassa"])
	}
	if got["yookassa"]["test"] != "1" || enabled["yookassa"] != 1 {
		t.Errorf("YooKassa flags did not carry over: config=%+v enabled=%d", got["yookassa"], enabled["yookassa"])
	}
	if got["cryptobot"]["token"] != "tok-123" || got["cryptobot"]["testnet"] != "" {
		t.Errorf("the CryptoBot token was lost: %+v", got["cryptobot"])
	}
}

// A provider the operator has already configured in the new form must NOT be
// overwritten by whatever is left in the old columns — that configuration is the
// current one.
func TestDeadColumnsKeepExistingProviderRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "both.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE settings ADD COLUMN yookassa_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN yookassa_shop_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN yookassa_secret_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN yookassa_test INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN cryptobot_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN cryptobot_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN cryptobot_testnet INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN trojan_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN trojan_port INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN trojan_fp TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN trojan_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE settings ADD COLUMN ws_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN trojan_enabled INTEGER`,
		`UPDATE settings SET yookassa_shop_id = 'stale', yookassa_secret_key = 'stale' WHERE id = 1`,
		`INSERT INTO payment_providers (key, enabled, config) VALUES ('yookassa', 1, '{"shop_id":"current"}')`,
		`DELETE FROM schema_migrations WHERE version = '0039_drop_dead_columns.sql'`,
	} {
		if _, err := st.db.Exec(stmt); err != nil {
			t.Fatalf("rewind (%s): %v", stmt, err)
		}
	}
	st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	var cfg string
	if err := st2.db.QueryRow(`SELECT config FROM payment_providers WHERE key = 'yookassa'`).Scan(&cfg); err != nil {
		t.Fatalf("read provider: %v", err)
	}
	if cfg != `{"shop_id":"current"}` {
		t.Errorf("the configured provider was overwritten by a stale column: %s", cfg)
	}
}
