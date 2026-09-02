package importer

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixtureDB(t *testing.T, name string, stmts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return path
}

func TestParseMarzbanDatabase(t *testing.T) {
	path := fixtureDB(t, "db.sqlite3",
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, status TEXT, used_traffic INTEGER,
			data_limit INTEGER, expire INTEGER, note TEXT, created_at TEXT)`,
		`CREATE TABLE proxies (id INTEGER PRIMARY KEY, user_id INTEGER, type TEXT, settings TEXT)`,
		`INSERT INTO users VALUES
			(1, 'alice', 'active', 123456, 10737418240, 1900000000, 'from telegram', ''),
			(2, 'bob', 'disabled', 0, NULL, NULL, NULL, ''),
			(3, 'carol', 'limited', 5, 100, 0, '', '')`,
		`INSERT INTO proxies VALUES
			(1, 1, 'VLESS', '{"id": "6BBD16CD-DFC1-47C4-9426-59B57B92B173", "flow": "xtls-rprx-vision"}'),
			(2, 1, 'Trojan', '{"password": "s3cret"}'),
			(3, 2, 'VMess', '{"id": "1fa689ce-9afd-415e-b250-dcf755ff8b4e"}'),
			(4, 3, 'Shadowsocks', '{"password": "sspw", "method": "chacha20-ietf-poly1305"}')`,
	)
	src, users, err := Parse(path)
	if err != nil || src != SourceMarzban {
		t.Fatalf("parse: %v (%s)", err, src)
	}
	if len(users) != 3 {
		t.Fatalf("want 3 users, got %d", len(users))
	}
	a := users[0]
	if a.Name != "alice" || a.UUID != "6bbd16cd-dfc1-47c4-9426-59b57b92b173" || a.Password != "s3cret" ||
		a.DataLimit != 10737418240 || a.ExpireAt != 1900000000 || a.UsedDown != 123456 ||
		!a.Enabled || a.Note != "from telegram" || len(a.Issues) != 0 {
		t.Errorf("alice: %+v", a)
	}
	b := users[1]
	if b.Enabled || b.UUID != "1fa689ce-9afd-415e-b250-dcf755ff8b4e" || b.DataLimit != 0 || b.ExpireAt != 0 {
		t.Errorf("bob: %+v", b)
	}
	if !reflect.DeepEqual(b.Issues, []string{IssuePasswordGenerated}) || b.Password == "" {
		t.Errorf("bob should have a generated password flagged: %+v", b)
	}
	c := users[2]
	if !c.Enabled || c.Password != "sspw" || !reflect.DeepEqual(c.Issues, []string{IssueUUIDGenerated}) {
		t.Errorf("carol (limited, ss only): %+v", c)
	}
	// Reading must leave no trace next to the file: no journal, no WAL.
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("parsing wrote next to the database: %v", entries)
	}
}

func TestParseMarzbanJSONDump(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"wrapped.json": `{"users": [{"username": "dave", "status": "active", "used_traffic": 7,
			"data_limit": 0, "expire": null, "note": "n",
			"subscription_url": "/sub/eyJhbGciOiJIUzI1NiJ9.token",
			"proxies": {"vless": {"id": "73ab3f5f-209b-4423-9fad-263e5baa37c4"}, "trojan": {"password": "p"}}}],
			"total": 1}`,
		"bare.json": `[{"username": "dave", "status": "on_hold",
			"proxies": {"vmess": {"id": "73ab3f5f-209b-4423-9fad-263e5baa37c4"}}}]`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		src, users, err := Parse(path)
		if err != nil || src != SourceMarzban || len(users) != 1 {
			t.Fatalf("%s: %v %s %d", name, err, src, len(users))
		}
		if users[0].Name != "dave" || users[0].UUID != "73ab3f5f-209b-4423-9fad-263e5baa37c4" || !users[0].Enabled {
			t.Errorf("%s: %+v", name, users[0])
		}
		// The dump spells the subscription URL out, so the token moves with the user;
		// a database import has none to move (Marzban signs it rather than storing it).
		want := ""
		if name == "wrapped.json" {
			want = "eyJhbGciOiJIUzI1NiJ9.token"
		}
		if users[0].SubToken != want {
			t.Errorf("%s: sub token %q, want %q", name, users[0].SubToken, want)
		}
	}
}

func TestParseXUIDatabase(t *testing.T) {
	path := fixtureDB(t, "x-ui.db",
		`CREATE TABLE inbounds (id INTEGER PRIMARY KEY, protocol TEXT, settings TEXT, enable INTEGER, remark TEXT)`,
		`CREATE TABLE client_traffics (id INTEGER PRIMARY KEY, inbound_id INTEGER, enable INTEGER, email TEXT,
			up INTEGER, down INTEGER, expiry_time INTEGER, total INTEGER)`,
		`INSERT INTO inbounds VALUES
			(1, 'vless', '{"clients": [
				{"id": "4372c397-2284-4cdd-9092-cfd0edc32092", "email": "eve", "subId": "abc123subid", "limitIp": 2, "totalGB": 5368709120, "expiryTime": 1900000000000, "enable": true, "comment": "vip client"},
				{"id": "b6e0a97f-cb56-42b3-ad5b-7e8eb3e872f0", "email": "frank", "limitIp": 0, "totalGB": 0, "expiryTime": -2592000000, "enable": false}
			]}', 1, 'main'),
			(2, 'trojan', '{"clients": [{"password": "trojanpw", "email": "eve", "totalGB": 0, "expiryTime": 0}]}', 1, 'tr')`,
		`INSERT INTO client_traffics VALUES
			(1, 1, 1, 'eve', 1000, 2000, 1900000000000, 5368709120),
			(2, 2, 1, 'eve', 10, 20, 0, 0),
			(3, 1, 0, 'frank', 0, 0, 0, 0)`,
	)
	src, users, err := Parse(path)
	if err != nil || src != SourceXUI {
		t.Fatalf("parse: %v (%s)", err, src)
	}
	if len(users) != 2 {
		t.Fatalf("want 2 users (eve folded across two inbounds), got %d", len(users))
	}
	eve := users[0]
	if eve.Name != "eve" || eve.UUID != "4372c397-2284-4cdd-9092-cfd0edc32092" || eve.Password != "trojanpw" ||
		eve.DeviceLimit != 2 || eve.DataLimit != 5368709120 || eve.ExpireAt != 1900000000 ||
		eve.UsedUp != 1010 || eve.UsedDown != 2020 || !eve.Enabled || eve.Note != "vip client" ||
		eve.SubToken != "abc123subid" || len(eve.Issues) != 0 {
		t.Errorf("eve: %+v", eve)
	}
	frank := users[1]
	if frank.Enabled || frank.ExpireAt != 0 || frank.Password == "" {
		t.Errorf("frank: %+v", frank)
	}
	if !reflect.DeepEqual(frank.Issues, []string{IssueExpiryRelative, IssuePasswordGenerated}) {
		t.Errorf("frank should carry the relative-expiry and generated-password flags: %v", frank.Issues)
	}
}

func TestParseRefusesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"text.txt":   "hello",
		"other.json": `{"settings": {"x": 1}}`,
		"empty":      "",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Parse(path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A SQLite database of some unrelated program.
	other := fixtureDB(t, "other.db", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`)
	if _, _, err := Parse(other); err == nil {
		t.Error("an unrelated database was accepted")
	}
}

// This panel's own export is read back with everything in it — the credentials
// that keep a user's apps working, the limits, the usage and the annotations.
func TestParseRosPanelExport(t *testing.T) {
	dir := t.TempDir()
	body := `{"format": "rospanel-users", "version": 1, "exported_at": 1780000000, "panel": "old.example.com",
		"users": [{"name": "moved", "uuid": "0f8fad5b-d9cb-469f-a165-70867728950e", "password": "pw",
			"data_limit": 100, "expire_at": 200, "used_up": 5, "used_down": 6, "device_limit": 3,
			"enabled": false, "note": "n", "sub_token": "tok-1", "wg_private_key": "wgkey",
			"speed_limit": 2048, "reset_period": "monthly", "tags": ["Vip", "vip"]}]}`
	path := filepath.Join(dir, "export.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	src, users, err := Parse(path)
	if err != nil || src != SourceRosPanel || len(users) != 1 {
		t.Fatalf("parse: %v %s %d", err, src, len(users))
	}
	u := users[0]
	if u.Name != "moved" || u.UUID != "0f8fad5b-d9cb-469f-a165-70867728950e" || u.Password != "pw" ||
		u.DataLimit != 100 || u.ExpireAt != 200 || u.UsedUp != 5 || u.UsedDown != 6 ||
		u.DeviceLimit != 3 || u.Enabled || u.Note != "n" || u.SubToken != "tok-1" ||
		u.WGPrivate != "wgkey" || u.SpeedLimit != 2048 || u.ResetPeriod != "monthly" ||
		len(u.Tags) != 2 || len(u.Issues) != 0 {
		t.Errorf("round trip lost something: %+v", u)
	}

	// A file from a newer panel is refused rather than half-read.
	newer := filepath.Join(dir, "newer.json")
	if err := os.WriteFile(newer, []byte(`{"format": "rospanel-users", "version": 99, "users": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Parse(newer); err == nil {
		t.Error("a newer export version was accepted")
	}
	// Without the marker it is somebody else's JSON and must not read as ours.
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte(`{"version": 1, "users": [{"username": "x", "proxies": {}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if src, _, _ := Parse(other); src == SourceRosPanel {
		t.Error("a foreign file was read as this panel's export")
	}
}
