package store

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// ReencryptSensitiveFields names its columns as STRINGS, so nothing but this test
// notices when one is renamed or dropped by a migration. It matters more than it
// looks: the function bails on the first bad column, so a stale name doesn't just
// skip that field — it silently stops re-encrypting every secret after it, and the
// panel logs one warning at startup and carries on.
func TestReencryptCoversItsColumns(t *testing.T) {
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

func TestReencryptAllTables(t *testing.T) {
	st := newStore(t)

	// 1. Insert plaintext secrets across nodes, webhooks, inbounds, admins
	_, err := st.db.Exec(`INSERT INTO nodes (name, host, reality_private_key, warp_private_key, zerossl_eab_hmac, proxy_accounts)
		VALUES ('n1', '1.2.3.4', 'plain-reality-priv', 'plain-warp-priv', 'plain-hmac', '[{"user":"nodeuser","pass":"nodepass"}]')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	_, err = st.db.Exec(`INSERT INTO webhooks (url, secret, events, enabled, created_at)
		VALUES ('https://example.com/hook', 'plain-webhook-secret', 'user.created', 1, 1000)`)
	if err != nil {
		t.Fatalf("insert webhook: %v", err)
	}

	_, err = st.db.Exec(`INSERT INTO inbounds (server_id, enabled, sort, name, protocol, port, opts, created_at)
		VALUES (0, 1, 1, 'vless-in', 'vless', 8443, '{"reality_private_key":"plain-inbound-priv"}', 1000)`)
	if err != nil {
		t.Fatalf("insert inbound: %v", err)
	}

	_, err = st.db.Exec(`INSERT INTO admins (username, password_hash, role, totp_secret, totp_pending)
		VALUES ('testadmin', 'hash', 'admin', 'plain-totp-secret', 'plain-totp-pending')`)
	if err != nil {
		t.Fatalf("insert admin: %v", err)
	}

	// 2. Run ReencryptSensitiveFields
	if err := st.ReencryptSensitiveFields(); err != nil {
		t.Fatalf("reencrypt: %v", err)
	}

	// 3. Verify nodes
	var nodeReality, nodeWarp, nodeHMAC, nodeProxy string
	if err := st.db.QueryRow(`SELECT reality_private_key, warp_private_key, zerossl_eab_hmac, proxy_accounts FROM nodes WHERE name = 'n1'`).
		Scan(&nodeReality, &nodeWarp, &nodeHMAC, &nodeProxy); err != nil {
		t.Fatalf("query node: %v", err)
	}
	if !strings.HasPrefix(nodeReality, "enc:v1:") || !strings.HasPrefix(nodeWarp, "enc:v1:") || !strings.HasPrefix(nodeHMAC, "enc:v1:") {
		t.Errorf("node keys not encrypted: reality=%s warp=%s hmac=%s", nodeReality, nodeWarp, nodeHMAC)
	}
	if !strings.Contains(nodeProxy, "enc:v1:") {
		t.Errorf("node proxy accounts not encrypted: %s", nodeProxy)
	}

	// 4. Verify webhooks
	var hookSec string
	if err := st.db.QueryRow(`SELECT secret FROM webhooks WHERE url = 'https://example.com/hook'`).Scan(&hookSec); err != nil {
		t.Fatalf("query webhook: %v", err)
	}
	if !strings.HasPrefix(hookSec, "enc:v1:") {
		t.Errorf("webhook secret not encrypted: %s", hookSec)
	}

	// 5. Verify inbounds
	var inOpts string
	if err := st.db.QueryRow(`SELECT opts FROM inbounds WHERE name = 'vless-in'`).Scan(&inOpts); err != nil {
		t.Fatalf("query inbound: %v", err)
	}
	if !strings.Contains(inOpts, "enc:v1:") {
		t.Errorf("inbound opts not encrypted: %s", inOpts)
	}

	// 6. Verify admins
	var adminTotp, adminPending string
	if err := st.db.QueryRow(`SELECT totp_secret, totp_pending FROM admins WHERE username = 'testadmin'`).
		Scan(&adminTotp, &adminPending); err != nil {
		t.Fatalf("query admin: %v", err)
	}
	if !strings.HasPrefix(adminTotp, "enc:v1:") || !strings.HasPrefix(adminPending, "enc:v1:") {
		t.Errorf("admin totp not encrypted: totp=%s pending=%s", adminTotp, adminPending)
	}

	// 7. Verify decryption reads back original plaintext
	node, err := st.GetNode(1)
	if err != nil {
		t.Fatalf("read node: %v", err)
	}
	if node.RealityPrivateKey != "plain-reality-priv" || node.WarpPrivateKey != "plain-warp-priv" || node.ZeroSSLEABHMAC != "plain-hmac" {
		t.Errorf("node secrets did not decrypt properly: %+v", node)
	}
}

