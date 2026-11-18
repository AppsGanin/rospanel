package store

import (
	"strings"
	"testing"
	"time"
)

// cloneUsers copies user tmpl n times in SQL, with its sightings, and makes the
// unique columns distinct. Creating tens of thousands of accounts one by one takes
// minutes; this takes a second.
func cloneUsers(t *testing.T, st *Store, tmpl int64, n int) {
	t.Helper()
	columns := func(table string) []string {
		rows, err := st.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			out = append(out, c)
		}
		return out
	}
	unique := map[string]string{
		"uuid": "uuid || '-' || n", "sub_token": "sub_token || '-' || n",
		"awg_slot": "0", "ref_code": "''", "external_id": "''",
	}
	var cols, exprs []string
	for _, c := range columns("users") {
		if c == "id" {
			continue
		}
		cols = append(cols, c)
		if e, ok := unique[c]; ok {
			exprs = append(exprs, e)
		} else {
			exprs = append(exprs, c)
		}
	}
	if _, err := st.db.Exec(`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < ?)
		INSERT INTO users (`+strings.Join(cols, ", ")+`)
		SELECT `+strings.Join(exprs, ", ")+` FROM users, seq WHERE id = ?`, n, tmpl); err != nil {
		t.Fatalf("clone users: %v", err)
	}
	var ccols, cexprs []string
	for _, c := range columns("connections") {
		ccols = append(ccols, c)
		if c == "user_id" {
			cexprs = append(cexprs, "u.id")
		} else {
			cexprs = append(cexprs, "c."+c)
		}
	}
	if _, err := st.db.Exec(`INSERT INTO connections (`+strings.Join(ccols, ", ")+`)
		SELECT `+strings.Join(cexprs, ", ")+` FROM connections c, users u
		WHERE c.user_id = ? AND u.id <> ?`, tmpl, tmpl); err != nil {
		t.Fatalf("clone connections: %v", err)
	}
}

// More users cross the device limit in one tick than SQLite binds parameters in one
// statement (32766): every one of them is armed, and every one disarmed once the
// extra devices are gone.
func TestStampDeviceOverLimitPastTheParameterLimit(t *testing.T) {
	t.Parallel()
	st := dcStore(t)
	now := time.Now().Unix()
	const n = 33000
	u := dcUser(t, st, "tmpl",
		ConnectionHit{IP: "10.0.0.1", SeenAt: now}, ConnectionHit{IP: "10.0.0.2", SeenAt: now})
	cloneUsers(t, st, u.ID, n-1)
	stamped := func() (c int) {
		t.Helper()
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM users WHERE device_over_since <> 0`).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if err := st.StampDeviceOverLimit(now); err != nil {
		t.Fatal(err)
	}
	if got := stamped(); got != n {
		t.Fatalf("armed %d of %d", got, n)
	}
	if _, err := st.db.Exec(`DELETE FROM connections WHERE ip = '10.0.0.2'`); err != nil {
		t.Fatal(err)
	}
	if err := st.StampDeviceOverLimit(now); err != nil {
		t.Fatal(err)
	}
	if got := stamped(); got != 0 {
		t.Fatalf("%d still armed after the extra devices left", got)
	}
}
