package store

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// dbBeforeMigration builds a database at path the way a box that had not yet received
// migration `before` (a "0081"-style prefix) would hold it: every earlier migration
// applied and recorded, nothing after. The caller seeds what the upgrade has to carry,
// closes the handle and Opens the path to run the rest.
func dbBeforeMigration(t *testing.T, path, before string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL DEFAULT (unixepoch()))`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && e.Name() < before {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for _, name := range files {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// {flag} and {country} left the name variables. Names that carried them lose them on
// the upgrade — unless what is left is a name validation would refuse, which keeps its
// braces rather than putting two connections under one name in every client's profile.
func TestCountryNameVarsMigrationStripsNames(t *testing.T) {
	t.Parallel()
	type inbound struct {
		server     int64
		name, want string
	}
	cases := []struct {
		name  string
		lanes [4]string // vless, reality, hysteria2, awg: before
		want  [4]string // … and after the upgrade
		inb   []inbound
	}{
		{
			name:  "names",
			lanes: [4]string{"Main", "{country} main", "{flag} Резерв", "{flag}"},
			// reality would take the VLESS lane's name, hysteria2 an inbound's; awg is
			// left with nothing and goes back to its default label.
			want: [4]string{"Main", "{country} main", "{flag} Резерв", ""},
			inb: []inbound{
				{0, "{flag} VLESS  {country} ({left})", "VLESS ({left})"},
				{0, "A {flag} {country} B", "A B"},
				{0, "Резерв", "Резерв"},
				{0, "{country} Резерв", "{country} Резерв"}, // another inbound on this server holds it
				{0, "{flag} node b", "node b"},
				{0, "{country} NODE B", "{country} NODE B"}, // case-insensitive, like the index
				{0, "{flag} Node", "Node"},
				{2, "{flag} Node", "Node"},        // another server: no clash
				{0, "{flag}", "{flag}"},           // an inbound needs a name
				{0, "{flag} auto", "{flag} auto"}, // reserved
				{0, "{flag} main", "{flag} main"}, // the VLESS lane's name
				{0, "Plain name", "Plain name"},   // never carried either variable
			},
		},
		{
			// An inbound that takes a lane's default label first keeps the lane from
			// going back to it.
			name:  "default label taken",
			lanes: [4]string{"", "", "{flag}", ""},
			want:  [4]string{"", "", "{flag}", ""},
			inb:   []inbound{{0, "{country} HYSTERIA-UDP", "HYSTERIA-UDP"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pre-0081.db")
			db := dbBeforeMigration(t, path, "0081")
			if _, err := db.Exec(`UPDATE settings SET vless_name = ?, reality_name = ?, hysteria_name = ?, awg_name = ? WHERE id = 1`,
				c.lanes[0], c.lanes[1], c.lanes[2], c.lanes[3]); err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, len(c.inb))
			for i, in := range c.inb {
				res, err := db.Exec(`INSERT INTO inbounds (server_id, name, protocol, port) VALUES (?, ?, 'vless', ?)`,
					in.server, in.name, 20000+i)
				if err != nil {
					t.Fatalf("insert %q: %v", in.name, err)
				}
				ids[i], _ = res.LastInsertId()
			}
			db.Close()

			st, err := Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer st.Close()

			for i, in := range c.inb {
				var got string
				if err := st.db.QueryRow(`SELECT name FROM inbounds WHERE id = ?`, ids[i]).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != in.want {
					t.Errorf("inbound %q on server %d: %q after the upgrade, want %q", in.name, in.server, got, in.want)
				}
			}
			var got [4]string
			if err := st.db.QueryRow(`SELECT vless_name, reality_name, hysteria_name, awg_name FROM settings WHERE id = 1`).
				Scan(&got[0], &got[1], &got[2], &got[3]); err != nil {
				t.Fatal(err)
			}
			for i, lane := range []string{"vless", "reality", "hysteria2", "awg"} {
				if got[i] != c.want[i] {
					t.Errorf("%s lane name: %q after the upgrade, want %q", lane, got[i], c.want[i])
				}
			}
		})
	}
}
