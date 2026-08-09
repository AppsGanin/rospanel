package store

import (
	"errors"
	"strings"
	"testing"
)

// A TOTP seed is a password equivalent: whoever reads it can mint valid codes
// forever. It has to be encrypted at rest like every other secret in this database —
// and it has to come back out intact, or every admin who enabled 2FA is locked out.
func TestAdminTOTPSecretEncryptedAtRest(t *testing.T) {
	st := newStore(t)
	id, err := st.CreateAdmin("owner", "hash", "owner", false)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if err := st.EnableAdminTOTP(id, secret, 42); err != nil {
		t.Fatalf("enable: %v", err)
	}
	var raw string
	if err := st.db.QueryRow(`SELECT totp_secret FROM admins WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatalf("read raw: %v", err)
	}
	if strings.Contains(raw, secret) {
		t.Errorf("the second-factor secret is stored in clear: %s", raw)
	}
	got, err := st.AdminTOTPByID(id)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Secret != secret || got.LastStep != 42 || !got.Enabled() {
		t.Fatalf("state did not round-trip: %+v", got)
	}

	// A half-finished setup is encrypted too — it is the same secret, one confirmation
	// away from being live.
	if err := st.SetAdminTOTPPending(id, secret); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if err := st.db.QueryRow(`SELECT totp_pending FROM admins WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatalf("read raw pending: %v", err)
	}
	if strings.Contains(raw, secret) {
		t.Errorf("the pending secret is stored in clear: %s", raw)
	}
}

// Claiming a step is what makes a code one-time: exactly one caller may win a given
// step, the marker only ever moves forward, and a claim that loses says so — the login
// refuses on a lost claim, so a claim that lied would hand out a second session for a
// code that was already spent.
func TestMarkAdminTOTPStepClaimsOnce(t *testing.T) {
	st := newStore(t)
	id, _ := st.CreateAdmin("owner", "hash", "owner", false)
	if err := st.EnableAdminTOTP(id, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", 100); err != nil {
		t.Fatalf("enable: %v", err)
	}
	won, err := st.MarkAdminTOTPStep(id, 90)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if won {
		t.Error("an older step was claimed — a spent code would sign in again")
	}
	got, _ := st.AdminTOTPByID(id)
	if got.LastStep != 100 {
		t.Errorf("last step went backwards to %d", got.LastStep)
	}
	if won, err = st.MarkAdminTOTPStep(id, 101); err != nil || !won {
		t.Fatalf("mark forward: won=%v err=%v", won, err)
	}
	if got, _ = st.AdminTOTPByID(id); got.LastStep != 101 {
		t.Errorf("last step did not advance: %d", got.LastStep)
	}
	// The same step twice is the race the login guards on: the second caller must be
	// told it lost rather than quietly reusing the code.
	if won, err = st.MarkAdminTOTPStep(id, 101); err != nil {
		t.Fatalf("mark again: %v", err)
	}
	if won {
		t.Error("the same step was claimed twice — the code is not one-time")
	}
}

// A secret that cannot be decrypted must be an ERROR, not an empty secret: empty reads
// as "this admin has no second factor", which would sign them in on the password alone
// — the login silently weakening itself is worse than the login failing.
func TestAdminTOTPUnreadableSecretIsAnError(t *testing.T) {
	st := newStore(t)
	id, _ := st.CreateAdmin("owner", "hash", "owner", false)
	if err := st.EnableAdminTOTP(id, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", 7); err != nil {
		t.Fatalf("enable: %v", err)
	}
	// What a wrong or replaced secrets.key looks like from here: an envelope that will
	// not open.
	if _, err := st.db.Exec(
		`UPDATE admins SET totp_secret = 'enc:v1:bm90LWEtcmVhbC1lbnZlbG9wZQ' WHERE id = ?`, id,
	); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	got, err := st.AdminTOTPByID(id)
	if !errors.Is(err, ErrTOTPUnreadable) {
		t.Fatalf("got (%+v, %v), want ErrTOTPUnreadable", got, err)
	}
	if got.Enabled() {
		t.Error("the returned state should be empty, not half-read")
	}
}
