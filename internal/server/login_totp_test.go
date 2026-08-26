package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/auth"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

// The second factor is only worth having if the LOGIN enforces it, so this drives the
// real /api/login through the real mux rather than testing the verifier again.

// tryLogin posts credentials and returns the status and the error code the panel
// answered with ("" on success).
func tryLogin(t *testing.T, rt *Router, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rt.panelMux().ServeHTTP(w, req)
	// The panel's error envelope is flat: {"error": "<text>", "code": "err.…"}.
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env.Code
}

// enrolTOTP switches a second factor on for an admin, the way the panel does.
func enrolTOTP(t *testing.T, st *store.Store, id int64) string {
	t.Helper()
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if err := st.EnableAdminTOTP(id, secret, 0); err != nil {
		t.Fatalf("enable: %v", err)
	}
	return secret
}

func codeNow(t *testing.T, secret string) string {
	t.Helper()
	code, err := auth.TOTPCodeAt(secret, time.Now())
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	return code
}

// The whole point: with 2FA on, the password alone must not produce a session — and
// the panel has to SAY it wants a code, or the login screen cannot ask for one.
func TestLoginRequiresTOTPCode(t *testing.T) {
	rt, st := rolesTestRouter(t)
	hash, err := auth.HashPassword("a-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	id, err := st.CreateAdmin("owner", hash, model.RoleOwner, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	secret := enrolTOTP(t, st, id)

	code, errCode := tryLogin(t, rt, `{"username":"owner","password":"a-password"}`)
	if code != http.StatusUnauthorized || errCode != "err.totpRequired" {
		t.Fatalf("password alone: %d %s — want 401 err.totpRequired", code, errCode)
	}
	if code, errCode = tryLogin(t, rt, `{"username":"owner","password":"a-password","code":"000000"}`); code != http.StatusUnauthorized || errCode != "err.totpInvalid" {
		t.Fatalf("wrong code: %d %s — want 401 err.totpInvalid", code, errCode)
	}
	// A wrong PASSWORD must still look exactly like it did before: the code field
	// mustn't turn the login into an oracle for "this account exists".
	if code, errCode = tryLogin(t, rt, `{"username":"owner","password":"nope","code":"`+codeNow(t, secret)+`"}`); errCode != "err.badCredentials" {
		t.Fatalf("wrong password: %d %s — want err.badCredentials", code, errCode)
	}

	good := codeNow(t, secret)
	if code, errCode = tryLogin(t, rt, `{"username":"owner","password":"a-password","code":"`+good+`"}`); code != http.StatusOK {
		t.Fatalf("valid code refused: %d %s", code, errCode)
	}

	// And that code is spent: replaying it inside the same 30-second window must not
	// open a second session.
	if code, errCode = tryLogin(t, rt, `{"username":"owner","password":"a-password","code":"`+good+`"}`); code == http.StatusOK {
		t.Fatal("the same code signed in twice — the replay guard is not working")
	}
	if errCode != "err.totpInvalid" {
		t.Errorf("replay answered %s, want err.totpInvalid", errCode)
	}
}

// "Password right, code missing" is still an attempt. It has to be counted — reaching
// it costs a password hash, and an attacker who has the password and is held back only
// by the code would otherwise get an unbounded loop of them. The legitimate two-step
// sign-in must not suffer for it: the attempt that carries the code clears the count.
func TestLoginTOTPRequiredCountsTowardTheLockout(t *testing.T) {
	rt, st := rolesTestRouter(t)
	hash, _ := auth.HashPassword("a-password")
	id, err := st.CreateAdmin("owner", hash, model.RoleOwner, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	secret := enrolTOTP(t, st, id)

	// A normal two-step sign-in, twice over, must never trip the lockout.
	for i := 0; i < 3; i++ {
		if code, errCode := tryLogin(t, rt, `{"username":"owner","password":"a-password"}`); errCode != "err.totpRequired" {
			t.Fatalf("round %d step 1: %d %s", i, code, errCode)
		}
		body := `{"username":"owner","password":"a-password","code":"` + codeNow(t, secret) + `"}`
		if code, errCode := tryLogin(t, rt, body); code != http.StatusOK {
			t.Fatalf("round %d step 2: %d %s", i, code, errCode)
		}
		// Each round needs a code the server has not spent yet; rewind the replay marker
		// rather than sleeping 30 seconds three times.
		if err := st.EnableAdminTOTP(id, secret, auth.TOTPStep(time.Now())-1); err != nil {
			t.Fatalf("rewind guard: %v", err)
		}
	}

	// Abandoning at step one over and over is what gets throttled.
	var last string
	for i := 0; i < 12; i++ {
		_, last = tryLogin(t, rt, `{"username":"owner","password":"a-password"}`)
	}
	if last != "err.tooManyAttempts" {
		t.Errorf("twelve codeless attempts ended with %q, want the lockout", last)
	}
}

// An admin without a second factor keeps signing in with the password alone: turning
// the feature on for one account must not lock everyone else out.
func TestLoginWithoutTOTPUnchanged(t *testing.T) {
	rt, st := rolesTestRouter(t)
	hash, _ := auth.HashPassword("a-password")
	if _, err := st.CreateAdmin("plain", hash, model.RoleAdmin, false); err != nil {
		t.Fatalf("create: %v", err)
	}
	if code, errCode := tryLogin(t, rt, `{"username":"plain","password":"a-password"}`); code != http.StatusOK {
		t.Fatalf("plain login: %d %s", code, errCode)
	}
	// A code sent to an account that has no second factor is ignored, not an error.
	if code, _ := tryLogin(t, rt, `{"username":"plain","password":"a-password","code":"123456"}`); code != http.StatusOK {
		t.Errorf("an unnecessary code broke the login: %d", code)
	}
}

// The roster must say WHETHER an admin has a second factor and nothing more — the
// secret itself never leaves the server after setup. (That it is also encrypted at
// rest is checked in the store package, which can read the raw column.)
func TestTOTPSecretNeverLeavesInTheRoster(t *testing.T) {
	_, st := rolesTestRouter(t)
	hash, _ := auth.HashPassword("a-password")
	id, _ := st.CreateAdmin("owner", hash, model.RoleOwner, false)
	secret := enrolTOTP(t, st, id)

	admins, err := st.ListAdmins()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	body, _ := json.Marshal(admins)
	if strings.Contains(string(body), secret) {
		t.Errorf("the secret leaks through the admin roster: %s", body)
	}
	if !admins[0].TOTPEnabled {
		t.Error("the roster does not show that this admin has a second factor")
	}
}

// The escape hatch: the CLI clears a second factor for an admin who lost their phone,
// and reports honestly when the login does not exist.
func TestDisableTOTPByName(t *testing.T) {
	_, st := rolesTestRouter(t)
	hash, _ := auth.HashPassword("a-password")
	id, _ := st.CreateAdmin("owner", hash, model.RoleOwner, false)
	enrolTOTP(t, st, id)

	ok, err := st.DisableAdminTOTPByName("owner")
	if err != nil || !ok {
		t.Fatalf("reset: ok=%v err=%v", ok, err)
	}
	t2, _ := st.AdminTOTPByID(id)
	if t2.Enabled() || t2.Pending != "" || t2.LastStep != 0 {
		t.Errorf("state survived the reset: %+v", t2)
	}
	if ok, _ := st.DisableAdminTOTPByName("nobody"); ok {
		t.Error("resetting a non-existent admin reported success")
	}
}
