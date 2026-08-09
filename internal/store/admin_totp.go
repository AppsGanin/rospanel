package store

import (
	"database/sql"
	"errors"
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
