package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A database restored from a backup written before a field was encrypted — or from
// an install that never had encryption on — carries plaintext secrets. The sweep
// wraps every one of them, and a secret it cannot read back is left alone rather
// than replaced by a blob nobody can decrypt.
func TestReencryptCoversEverySecretColumn(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "enc.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// Plant plaintext where each writer would have put an envelope.
	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok-1", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE users SET password = 'plain-pw', wg_private_key = 'plain-wg' WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	adminID, err := st.CreateAdmin("owner", "hash", model.RoleOwner, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE admins SET totp_secret = 'plain-totp', totp_pending = 'plain-pending' WHERE id = ?`, adminID); err != nil {
		t.Fatal(err)
	}
	n, err := st.CreateNode("edge", "203.0.113.10", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE nodes SET reality_private_key = 'plain-reality', warp_private_key = 'plain-warp',
		awg_private_key = 'plain-awg', zerossl_eab_hmac = 'plain-eab' WHERE id = ?`, n.ID); err != nil {
		t.Fatal(err)
	}
	w, err := st.CreateWebhook("https://example.com/hook", []string{model.WebhookUserCreated}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE webhooks SET secret = 'plain-hook' WHERE id = ?`, w.ID); err != nil {
		t.Fatal(err)
	}
	in, err := st.CreateInbound(model.Inbound{
		ServerID: model.LocalNodeID, Name: "extra", Protocol: model.InbVLESS, Port: 2053, Enabled: true,
		Opts: model.InboundOpts{Transport: model.TrTCP, Security: model.SecReality, RealityPrivateKey: "plain-inb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE inbounds SET opts = ? WHERE id = ?`,
		`{"transport":"tcp","security":"reality","reality_private_key":"plain-inb"}`, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE settings SET awg_private_key = 'plain-awg-master',
		proxy_accounts = ? WHERE id = 1`, `[{"user":"proxy","pass":"plain-proxy"}]`); err != nil {
		t.Fatal(err)
	}

	if err := st.ReencryptSensitiveFields(); err != nil {
		t.Fatalf("reencrypt: %v", err)
	}

	// Every one of them is now an envelope on disk…
	raw := func(query string, args ...any) string {
		var v string
		if err := st.db.QueryRow(query, args...).Scan(&v); err != nil {
			t.Fatalf("read back: %v", err)
		}
		return v
	}
	for _, c := range []struct{ what, got string }{
		{"user password", raw(`SELECT password FROM users WHERE id = ?`, u.ID)},
		{"user wg key", raw(`SELECT wg_private_key FROM users WHERE id = ?`, u.ID)},
		{"admin totp", raw(`SELECT totp_secret FROM admins WHERE id = ?`, adminID)},
		{"admin pending totp", raw(`SELECT totp_pending FROM admins WHERE id = ?`, adminID)},
		{"node reality key", raw(`SELECT reality_private_key FROM nodes WHERE id = ?`, n.ID)},
		{"node warp key", raw(`SELECT warp_private_key FROM nodes WHERE id = ?`, n.ID)},
		{"node awg key", raw(`SELECT awg_private_key FROM nodes WHERE id = ?`, n.ID)},
		{"node eab secret", raw(`SELECT zerossl_eab_hmac FROM nodes WHERE id = ?`, n.ID)},
		{"webhook secret", raw(`SELECT secret FROM webhooks WHERE id = ?`, w.ID)},
		{"master awg key", raw(`SELECT awg_private_key FROM settings WHERE id = 1`)},
	} {
		if !strings.HasPrefix(c.got, "enc:v1:") {
			t.Errorf("%s left in plaintext: %q", c.what, c.got)
		}
	}
	if opts := raw(`SELECT opts FROM inbounds WHERE id = ?`, in.ID); strings.Contains(opts, "plain-inb") {
		t.Errorf("inbound REALITY key left in plaintext: %s", opts)
	}
	var accs []model.SystemProxyAccount
	if err := json.Unmarshal([]byte(raw(`SELECT proxy_accounts FROM settings WHERE id = 1`)), &accs); err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 || !strings.HasPrefix(accs[0].Pass, "enc:v1:") {
		t.Errorf("system proxy password left in plaintext: %+v", accs)
	}

	// …and every reader still gets the value back.
	gotUser, err := st.GetUser(u.ID)
	if err != nil || gotUser.Password != "plain-pw" || gotUser.WGPrivateKey != "plain-wg" {
		t.Fatalf("user secrets not readable after the sweep: %+v (%v)", gotUser, err)
	}
	totp, err := st.AdminTOTPByID(adminID)
	if err != nil || totp.Secret != "plain-totp" || totp.Pending != "plain-pending" {
		t.Fatalf("second factor not readable: %+v (%v)", totp, err)
	}
	gotNode, err := st.GetNode(n.ID)
	if err != nil || gotNode.RealityPrivateKey != "plain-reality" || gotNode.WarpPrivateKey != "plain-warp" ||
		gotNode.AWGPrivateKey != "plain-awg" || gotNode.ZeroSSLEABHMAC != "plain-eab" {
		t.Fatalf("node keys not readable: %+v (%v)", gotNode, err)
	}
	hooks, err := st.ListWebhooks()
	if err != nil || len(hooks) != 1 || hooks[0].Secret != "plain-hook" {
		t.Fatalf("webhook secret not readable: %+v (%v)", hooks, err)
	}
	gotIn, err := st.GetInbound(in.ID)
	if err != nil || gotIn.Opts.RealityPrivateKey != "plain-inb" {
		t.Fatalf("inbound key not readable: %+v (%v)", gotIn, err)
	}
	set, err := st.GetSettings()
	if err != nil || set.AWGPrivateKey != "plain-awg-master" {
		t.Fatalf("master AWG key not readable: %v", err)
	}
	if len(set.ProxyAccounts) != 1 || set.ProxyAccounts[0].Pass != "plain-proxy" {
		t.Fatalf("system proxy password not readable: %+v", set.ProxyAccounts)
	}

	// Running it again changes nothing: an envelope is not re-wrapped.
	before := raw(`SELECT password FROM users WHERE id = ?`, u.ID)
	if err := st.ReencryptSensitiveFields(); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if after := raw(`SELECT password FROM users WHERE id = ?`, u.ID); after != before {
		t.Error("an already-encrypted secret was wrapped twice")
	}
}
