package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/AppsGanin/rospanel/internal/datasec"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The admin second factor. The secret is encrypted at rest like every other secret in
// this database; the replay marker beside it is not one, and is stored in clear so an
// operator debugging "why was my code refused" can read it.

// AdminTOTP is one admin's second-factor state.
type AdminTOTP struct {
	Secret   string // confirmed shared secret; "" ⇒ no second factor
	Pending  string // secret being set up, not yet proved with a live code
	LastStep int64  // last accepted time step (the one-time guard)
}

// Enabled reports whether this admin must present a code to sign in.
func (t AdminTOTP) Enabled() bool { return t.Secret != "" }

// ErrTOTPUnreadable means the stored second factor could not be decrypted (a wrong or
// replaced secrets.key, a corrupted column). It exists because the alternative is
// worse than an error: decField answers "" on a failed decrypt, and an empty secret
// reads as "this admin has no second factor" — so the login would quietly wave
// through the password alone on exactly the accounts that asked for more than that.
var ErrTOTPUnreadable = errors.New("second-factor secret could not be decrypted")

// AdminTOTPByID reads one admin's second-factor state.
func (s *Store) AdminTOTPByID(id int64) (AdminTOTP, error) {
	var t AdminTOTP
	err := s.db.QueryRow(
		`SELECT totp_secret, totp_pending, totp_last_step FROM admins WHERE id = ?`, id,
	).Scan(&t.Secret, &t.Pending, &t.LastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminTOTP{}, ErrAdminNotFound
	}
	if err != nil {
		return AdminTOTP{}, err
	}
	raw := t.Secret
	t.Secret, t.Pending = decField(t.Secret), decField(t.Pending)
	if raw != "" && t.Secret == "" {
		return AdminTOTP{}, ErrTOTPUnreadable
	}
	return t, nil
}

// SetAdminTOTPPending stores a secret that is being set up. It is deliberately NOT
// the same column as the live one: until the admin proves with a code that their app
// really holds it, the panel must keep letting them in with the password alone.
func (s *Store) SetAdminTOTPPending(id int64, secret string) error {
	_, err := s.db.Exec(
		`UPDATE admins SET totp_pending = ? WHERE id = ?`, encField(secret), id)
	return err
}

// EnableAdminTOTP promotes the pending secret to the live one. The step marker is
// carried in from the code that proved it, so the very code used to switch 2FA on
// cannot then be replayed to sign in.
func (s *Store) EnableAdminTOTP(id int64, secret string, step int64) error {
	_, err := s.db.Exec(
		`UPDATE admins SET totp_secret = ?, totp_pending = '', totp_last_step = ?
		 WHERE id = ?`, encField(secret), step, id)
	return err
}

// DisableAdminTOTP clears the second factor, including any half-finished setup.
func (s *Store) DisableAdminTOTP(id int64) error {
	_, err := s.db.Exec(
		`UPDATE admins SET totp_secret = '', totp_pending = '', totp_last_step = 0
		 WHERE id = ?`, id)
	return err
}

// DisableAdminTOTPByName is the escape hatch behind `rospanel totp reset <login>`,
// for the phone that was lost or wiped. It reports whether an admin matched, so the
// command can say "no such admin" instead of claiming success.
func (s *Store) DisableAdminTOTPByName(username string) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE admins SET totp_secret = '', totp_pending = '', totp_last_step = 0
		 WHERE username = ?`, username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MarkAdminTOTPStep claims a time step for a sign-in and reports whether this caller
// got it. The WHERE clause is the whole point: verification READS the marker and the
// write happens later, so two requests presenting the same code can both pass the
// check — and without a claim only one of them may proceed. The database decides, not
// the read, which also stops the older of two racing logins from pushing the marker
// backwards and reopening a code that was already spent.
func (s *Store) MarkAdminTOTPStep(id int64, step int64) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE admins SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`,
		step, id, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// BackupAdminTOTPSecrets reads the second-factor secrets of the admins in an unpacked
// backup who could restore a backup themselves — owner and admin; an operator cannot
// — decrypted under the backup's own secrets.key, not the running panel's.
//
// It is what lets a restore ask for the second factor OF THE BACKUP: a panel whose
// admins used 2FA should not be restorable by someone who holds the file but not the
// authenticator, and that includes a fresh install still in its first-run wizard,
// where there is no admin of this panel to ask anything of.
//
// Fails closed on anything it cannot read. A secret that will not decrypt — a backup
// with no secrets.key, a key that is not the one it was written with, a mangled
// column — is an error, never an empty result: an empty result means "this backup
// has no second factor", and answering that about one that does would wave the
// restore through on exactly the backups that asked for more.
func BackupAdminTOTPSecrets(dir string) ([]string, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "rospanel.db")+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// The owner's and the administrators' — the accounts that could restore that panel
	// under the ladder roles replaced. Restore is the owner's alone now, but narrowing
	// this to the owner would read a backup where only an administrator had 2FA as
	// having none, which is the one answer this must never give wrongly. An
	// operator's authenticator neither demands a code nor answers for one.
	rows, err := db.Query(
		`SELECT totp_secret FROM admins WHERE totp_secret != '' AND role IN (?, ?)`,
		model.RoleOwner, model.RoleAdmin)
	if err != nil {
		return nil, err
	}
	var enc []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, err
		}
		enc = append(enc, s)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(enc) == 0 {
		return nil, nil
	}

	// Only now is the key needed — a backup whose admins have no second factor, from
	// an install that never encrypted anything, may carry no key at all and is fine.
	key, keyErr := datasec.ReadKey(dir)
	out := make([]string, 0, len(enc))
	for _, e := range enc {
		plain, err := datasec.DecryptWith(key, e)
		if err != nil || plain == "" {
			if keyErr != nil {
				return nil, fmt.Errorf("%w: %v", ErrTOTPUnreadable, keyErr)
			}
			return nil, ErrTOTPUnreadable
		}
		out = append(out, plain)
	}
	return out, nil
}
