package auth

import (
	"strings"
	"testing"
	"time"
)

// rfcSecret is the RFC 6238 test key ("12345678901234567890") in base32 — the whole
// point of using it is that the expected codes below come from the RFC, not from this
// implementation. A hand-rolled TOTP that only agrees with itself would pass any test
// written from its own output and still be unreadable by every authenticator app.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTOTPMatchesRFC6238Vectors(t *testing.T) {
	// RFC 6238 Appendix B, the SHA-1 rows, truncated to the 6 digits we use.
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		got, err := TOTPCodeAt(rfcSecret, time.Unix(c.unix, 0).UTC())
		if err != nil {
			t.Fatalf("t=%d: %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("t=%d: code = %s, want %s (this secret must read the same in any authenticator app)", c.unix, got, c.want)
		}
	}
}

// A code is one-time. Without the replay guard it would be valid for the rest of its
// 30-second window — long enough for a code read over a shoulder, out of a screenshot
// or from a logging proxy to be used by someone else.
func TestVerifyTOTPRejectsReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	code, err := TOTPCodeAt(rfcSecret, now)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := VerifyTOTP(rfcSecret, code, now, 0)
	if !ok {
		t.Fatal("a fresh code was refused")
	}
	if _, ok := VerifyTOTP(rfcSecret, code, now, step); ok {
		t.Error("the same code was accepted twice")
	}
	// And nothing from before that step comes back either.
	prev, _ := TOTPCodeAt(rfcSecret, now.Add(-totpStep))
	if _, ok := VerifyTOTP(rfcSecret, prev, now, step); ok {
		t.Error("a code older than the last accepted step was accepted")
	}
}

// Phone clocks drift, so the neighbouring steps count — but only those.
func TestVerifyTOTPWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, d := range []time.Duration{-totpStep, 0, totpStep} {
		code, _ := TOTPCodeAt(rfcSecret, now.Add(d))
		if _, ok := VerifyTOTP(rfcSecret, code, now, 0); !ok {
			t.Errorf("offset %v was refused; a phone that far off is normal", d)
		}
	}
	for _, d := range []time.Duration{-2 * totpStep, 2 * totpStep} {
		code, _ := TOTPCodeAt(rfcSecret, now.Add(d))
		if _, ok := VerifyTOTP(rfcSecret, code, now, 0); ok {
			t.Errorf("offset %v was accepted; the window must not be that wide", d)
		}
	}
}

// Apps display the code as "123 456" and password managers copy the gap along with
// it. Refusing that is a support ticket, not security.
func TestVerifyTOTPAcceptsSpacedCode(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	code, _ := TOTPCodeAt(rfcSecret, now)
	spaced := " " + code[:3] + " " + code[3:] + "\n"
	if _, ok := VerifyTOTP(rfcSecret, spaced, now, 0); !ok {
		t.Errorf("a code copied with its space was refused: %q", spaced)
	}
}

func TestVerifyTOTPRejectsGarbage(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, c := range []string{"", "12345", "1234567", "abcdef", "  ", "000000"} {
		if _, ok := VerifyTOTP(rfcSecret, c, now, 0); ok {
			t.Errorf("%q was accepted", c)
		}
	}
	// A secret that is not base32 must fail closed, not panic.
	if _, ok := VerifyTOTP("not base32!!", "123456", now, 0); ok {
		t.Error("a malformed secret authenticated something")
	}
}

// The secret has to be the shape an app can take: uppercase base32, no padding, and
// long enough to be worth generating.
func TestGenerateTOTPSecretShape(t *testing.T) {
	s, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 32 { // 20 bytes in base32
		t.Errorf("secret length = %d, want 32 chars", len(s))
	}
	if strings.ContainsAny(s, "=abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("secret is not unpadded uppercase base32: %s", s)
	}
	if _, err := TOTPCodeAt(s, time.Now()); err != nil {
		t.Errorf("a freshly generated secret does not compute: %v", err)
	}
	// Two calls must not agree.
	if s2, _ := GenerateTOTPSecret(); s == s2 {
		t.Error("two generated secrets are identical")
	}
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("RosPanel", "admin", rfcSecret)
	for _, want := range []string{
		"otpauth://totp/RosPanel:admin?",
		"secret=" + rfcSecret,
		"issuer=RosPanel",
		"digits=6",
		"period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("URI missing %q: %s", want, uri)
		}
	}
}
