package store

import (
	"encoding/json"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The system proxy's accounts travel as one JSON column per server, with each
// PASSWORD individually wrapped in the at-rest envelope rather than the array being
// encrypted as a whole.
//
// Per field, because that is what let migration 0039 move the previous single
// (already-encrypted) password into the list verbatim — no decrypt/re-encrypt step in
// SQL, which SQLite could not have done anyway. It also keeps the logins readable in
// the database, which is what an operator debugging "which account is this?" wants.

// encodeProxyAccounts renders accounts for storage: JSON, passwords encrypted. An
// empty list is stored as "" rather than "[]" so a server that has never had a proxy
// looks exactly like one that has had its accounts removed.
func encodeProxyAccounts(accs []model.SystemProxyAccount) (string, error) {
	if len(accs) == 0 {
		return "", nil
	}
	out := make([]model.SystemProxyAccount, len(accs))
	for i, a := range accs {
		out[i] = model.SystemProxyAccount{User: a.User, Pass: encField(a.Pass)}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeProxyAccounts is the inverse. A malformed blob yields no accounts rather than
// an error: the listeners then generate with nothing to authenticate against, which
// the generator refuses to open at all — a closed proxy beats an open one.
func decodeProxyAccounts(raw string) []model.SystemProxyAccount {
	if raw == "" {
		return nil
	}
	var accs []model.SystemProxyAccount
	if err := json.Unmarshal([]byte(raw), &accs); err != nil {
		return nil
	}
	for i := range accs {
		accs[i].Pass = decField(accs[i].Pass)
	}
	return accs
}

// SetSystemProxy persists the master's forward-proxy listeners.
func (s *Store) SetSystemProxy(p model.SystemProxy) error {
	accs, err := encodeProxyAccounts(p.Accounts)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE settings SET proxy_socks_enabled = ?, proxy_socks_port = ?,
		        proxy_http_enabled = ?, proxy_http_port = ?, proxy_accounts = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		boolToInt(p.SocksEnabled), p.SocksPort,
		boolToInt(p.HTTPEnabled), p.HTTPPort, accs,
	)
	return err
}

// SetNodeSystemProxy persists a node's own forward-proxy listeners.
func (s *Store) SetNodeSystemProxy(id int64, p model.SystemProxy) error {
	accs, err := encodeProxyAccounts(p.Accounts)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE nodes SET proxy_socks_enabled = ?, proxy_socks_port = ?,
		        proxy_http_enabled = ?, proxy_http_port = ?, proxy_accounts = ?
		 WHERE id = ?`,
		boolToInt(p.SocksEnabled), p.SocksPort,
		boolToInt(p.HTTPEnabled), p.HTTPPort, accs, id,
	)
	return err
}
