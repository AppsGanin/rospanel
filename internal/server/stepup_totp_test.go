package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The two actions gated by a fresh second factor destroy something the panel cannot
// bring back: a factory reset wipes the database, and deleting a server cuts off
// everyone on it with no undo. These drive the real routes through the real mux,
// because the property that matters is "the handler refuses", not "the helper works".

// adminWithTOTP creates an owner with a second factor bound, and returns their
// session cookie plus the shared secret.
func adminWithTOTP(t *testing.T, st *store.Store, name string) (*http.Cookie, string) {
	t.Helper()
	c := signIn(t, st, name, model.RoleOwner, false)
	id, _, _, err := st.GetAdminAuth(name)
	if err != nil {
		t.Fatalf("lookup %s: %v", name, err)
	}
	return c, enrolTOTP(t, st, id)
}

// setupDone flips the first-run flag, because verifyStepUp waives re-authentication
// entirely while the wizard is still running.
func setupDone(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.SetSetupDone(true); err != nil {
		t.Fatalf("setup done: %v", err)
	}
}

// send drives one request and returns the status plus the panel's error code.
func send(t *testing.T, rt *Router, method, path, body string, c *http.Cookie, hdr map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	rt.panelMux().ServeHTTP(w, req)
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env.Code
}

// Deleting a server used to need nothing but a session cookie. It now needs the
// password, and a fresh code from whoever has an authenticator bound.
func TestDeleteNodeRequiresFreshTOTP(t *testing.T) {
	rt, st := rolesTestRouter(t)
	setupDone(t, st)
	cookie, secret := adminWithTOTP(t, st, "owner")

	// No credentials at all: the session alone is not enough any more.
	if code, errCode := send(t, rt, http.MethodDelete, "/api/nodes/1", "", cookie, nil); code != http.StatusForbidden {
		t.Fatalf("bare session: %d %s — want 403", code, errCode)
	}
	// Right password, no code.
	pw := map[string]string{"X-Current-Password": "a-password"}
	if code, errCode := send(t, rt, http.MethodDelete, "/api/nodes/1", "", cookie, pw); errCode != "err.totpRequired" {
		t.Fatalf("password alone: %d %s — want err.totpRequired", code, errCode)
	}
	// Right code, wrong password: the password is still checked first.
	bad := map[string]string{"X-Current-Password": "nope", "X-TOTP-Code": codeNow(t, secret)}
	if code, errCode := send(t, rt, http.MethodDelete, "/api/nodes/1", "", cookie, bad); errCode != "err.wrongPassword" {
		t.Fatalf("wrong password: %d %s — want err.wrongPassword", code, errCode)
	}
	// Wrong code, right password.
	wrong := map[string]string{"X-Current-Password": "a-password", "X-TOTP-Code": "000000"}
	if code, errCode := send(t, rt, http.MethodDelete, "/api/nodes/1", "", cookie, wrong); errCode != "err.totpInvalid" {
		t.Fatalf("wrong code: %d %s — want err.totpInvalid", code, errCode)
	}

	// Both right: the request gets past the gate. Node 1 does not exist in this store,
	// so what comes back is the manager's own answer — which is the point: the gate is
	// no longer what refuses it.
	good := codeNow(t, secret)
	ok := map[string]string{"X-Current-Password": "a-password", "X-TOTP-Code": good}
	code, errCode := send(t, rt, http.MethodDelete, "/api/nodes/1", "", cookie, ok)
	if errCode == "err.totpRequired" || errCode == "err.totpInvalid" || errCode == "err.wrongPassword" {
		t.Fatalf("valid credentials were refused by the gate: %d %s", code, errCode)
	}

	// And that code is spent. Replaying it inside the same 30-second window must not
	// authorise a second deletion — otherwise one shoulder-surfed code is worth every
	// server in the fleet.
	if code, errCode = send(t, rt, http.MethodDelete, "/api/nodes/1", "", cookie, ok); errCode != "err.totpUsed" {
		t.Errorf("replayed code answered %d %s, want err.totpUsed", code, errCode)
	}
}

// An admin with no authenticator keeps the password-only step-up: turning 2FA on for
// one account must not change how anyone else operates the panel.
func TestDeleteNodeWithoutTOTPNeedsOnlyThePassword(t *testing.T) {
	rt, st := rolesTestRouter(t)
	setupDone(t, st)
	cookie := signIn(t, st, "owner", model.RoleOwner, false)

	if code, errCode := send(t, rt, http.MethodDelete, "/api/nodes/1", "", cookie, nil); errCode != "err.wrongPassword" {
		t.Fatalf("no password: %d %s — want err.wrongPassword", code, errCode)
	}
	pw := map[string]string{"X-Current-Password": "a-password"}
	code, errCode := send(t, rt, http.MethodDelete, "/api/nodes/1", "", cookie, pw)
	if errCode == "err.wrongPassword" || errCode == "err.totpRequired" {
		t.Fatalf("password-only step-up was refused: %d %s", code, errCode)
	}
}

// The factory reset asks for the same pair. It had the password already; the code is
// what is new, and the reset must not run without it.
func TestFactoryResetRequiresFreshTOTP(t *testing.T) {
	rt, st := rolesTestRouter(t)
	setupDone(t, st)
	cookie, secret := adminWithTOTP(t, st, "owner")

	body := `{"current_password":"a-password"}`
	if code, errCode := send(t, rt, http.MethodPost, "/api/reset", body, cookie, nil); errCode != "err.totpRequired" {
		t.Fatalf("password alone: %d %s — want err.totpRequired", code, errCode)
	}
	bad := `{"current_password":"a-password","code":"000000"}`
	if code, errCode := send(t, rt, http.MethodPost, "/api/reset", bad, cookie, nil); errCode != "err.totpInvalid" {
		t.Fatalf("wrong code: %d %s — want err.totpInvalid", code, errCode)
	}

	// The reset itself is destructive and schedules a process restart, so this stops at
	// proving the gate opens: the step is claimed only after both credentials pass, so a
	// spent step is proof the handler was reached.
	good := codeNow(t, secret)
	id, _, _, err := st.GetAdminAuth("owner")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	step, ok := auth.VerifyTOTP(secret, good, time.Now(), 0)
	if !ok {
		t.Fatalf("the code we just generated does not verify")
	}
	claimed, err := st.MarkAdminTOTPStep(id, step)
	if err != nil || !claimed {
		t.Fatalf("pre-claim: claimed=%v err=%v", claimed, err)
	}
	// Now the same code is spent, which is exactly what a replay looks like.
	replay := `{"current_password":"a-password","code":"` + good + `"}`
	if code, errCode := send(t, rt, http.MethodPost, "/api/reset", replay, cookie, nil); errCode != "err.totpUsed" {
		t.Errorf("spent code answered %d %s, want err.totpUsed", code, errCode)
	}
}
