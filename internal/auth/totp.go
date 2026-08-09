package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Time-based one-time passwords for the admin login (RFC 6238), implemented here
// rather than pulled in: it is HMAC-SHA1 over a counter and a modulo, and a
// dependency for that would be more supply chain than code.
//
// SHA-1 and six digits are not a choice — they are what every authenticator app
// (Google Authenticator, Aegis, 1Password, Yandex Key) assumes when the otpauth URI
// omits the algorithm, and half of them ignore the parameter even when it is there.
// The security of TOTP does not rest on the hash: the secret never travels, and a
// code is worth 30 seconds.
const (
	totpStep    = 30 * time.Second
	totpDigits  = 6
	totpSkew    = 1  // accept the neighbouring steps: phone clocks drift
	totpSecrLen = 20 // 160 bits, the size RFC 4226 asks for
)

// base32NoPad is the alphabet authenticator apps expect: uppercase, unpadded.
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret mints a fresh shared secret in the base32 form an authenticator
// app takes.
func GenerateTOTPSecret() (string, error) {
	b := make([]byte, totpSecrLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32NoPad.EncodeToString(b), nil
}

// TOTPStep is the counter a moment falls into. Exposed because the replay guard
// stores it: a code is one-time, so the step that was accepted must never be
// accepted again — without that, a code shoulder-surfed (or read from a proxy log)
// stays usable for the rest of its 30-second window.
func TOTPStep(t time.Time) int64 { return t.Unix() / int64(totpStep.Seconds()) }

// totpCode computes the code for one step. Unexported: callers verify, they do not
// generate — except the tests, which use TOTPCodeAt.
func totpCode(secret string, step int64) (string, error) {
	key, err := base32NoPad.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("totp: bad secret: %w", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	// Dynamic truncation (RFC 4226 §5.3): the low nibble of the last byte picks the
	// 4-byte window, whose top bit is masked off to keep the number positive.
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000), nil
}

// TOTPCodeAt computes the code for a moment. Nothing in the panel signs in with a
// code it generated itself — this exists for the tests, which check the RFC's vectors
// and the replay guard.
func TOTPCodeAt(secret string, t time.Time) (string, error) {
	return totpCode(secret, TOTPStep(t))
}

// VerifyTOTP checks a code against the secret around time t and returns the step it
// matched. lastStep is the most recently ACCEPTED step for this admin; a code from
// that step or earlier is refused even when it is arithmetically valid, which is what
// makes a code one-time rather than one-per-30-seconds.
//
// The comparison is constant-time. It is a six-digit number, so a timing side channel
// is not the likely attack — but the cost of doing it right is one function call.
func VerifyTOTP(secret, code string, t time.Time, lastStep int64) (int64, bool) {
	// Authenticators show the code as "123 456" and password managers copy the space
	// with it, so whitespace anywhere in the code is dropped rather than rejected. The
	// panel's own field already strips it; this is for everything else that posts here.
	code = strings.Join(strings.Fields(code), "")
	if secret == "" || len(code) != totpDigits {
		return 0, false
	}
	now := TOTPStep(t)
	for step := now - totpSkew; step <= now+totpSkew; step++ {
		if step <= lastStep {
			continue // already used (or older than the last accepted one)
		}
		want, err := totpCode(secret, step)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// TOTPURI is the otpauth:// string an authenticator app scans. issuer shows up as
// the account's group in the app, account as its name — so an operator with several
// panels can tell them apart.
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{
		"secret": {secret},
		"issuer": {issuer},
		// Spelled out even though these are the defaults: apps that let a user edit an
		// entry show them, and an operator comparing two panels should not have to
		// guess which one is on six digits.
		"algorithm": {"SHA1"},
		"digits":    {fmt.Sprint(totpDigits)},
		"period":    {fmt.Sprint(int(totpStep.Seconds()))},
	}
	return "otpauth://totp/" + label + "?" + q.Encode()
}
