package store

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/datasec"
	"github.com/AppsGanin/rospanel/internal/model"
)

// ReencryptSensitiveFields names its columns as STRINGS, so nothing but this test
// notices when one is renamed or dropped by a migration. It matters more than it
// looks: the function bails on the first bad column, so a stale name doesn't just
// skip that field — it silently stops re-encrypting every secret after it, and the
// panel logs one warning at startup and carries on.
func TestReencryptCoversItsColumns(t *testing.T) {
	// At-rest encryption needs a key; without datasec.Init encField is a pass-through
	// and the roundtrip half of this test would silently prove nothing.
	if err := datasec.Init(t.TempDir()); err != nil {
		t.Fatalf("datasec: %v", err)
	}
	st := newStore(t)
	if err := st.ReencryptSensitiveFields(); err != nil {
		t.Fatalf("reencrypt on a fresh database: %v", err)
	}

	// With a plaintext secret in place (what a restore from an old backup leaves),
	// the pass must come back wrapped rather than untouched.
	if err := st.SetSystemProxy(model.SystemProxy{
		SocksEnabled: true, SocksPort: 1080,
		Accounts: []model.SystemProxyAccount{{User: "u", Pass: "secret-pass"}},
	}); err != nil {
		t.Fatalf("set proxy: %v", err)
	}
	var stored string
	if err := st.db.QueryRow(`SELECT proxy_accounts FROM settings WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(stored, "secret-pass") {
		t.Errorf("the proxy password is in the database in clear: %s", stored)
	}
	if !strings.Contains(stored, `"user":"u"`) {
		t.Errorf("the login should stay readable for debugging: %s", stored)
	}
	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if len(set.ProxyAccounts) != 1 || set.ProxyAccounts[0].Pass != "secret-pass" {
		t.Errorf("accounts did not round-trip: %+v", set.ProxyAccounts)
	}
}
