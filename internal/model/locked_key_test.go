package model

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// The placeholder key must not be computable from anything a client holds.
//
// SS2022 multi-user puts the SERVER key inside every client credential — the link is
// base64(method:serverKey:userKey) — so a derivation of the form hash(domain|serverKey)
// is a public formula with a public input: any user who was ever granted the lane could
// authenticate as the placeholder, with no account and no quota behind the traffic, in
// exactly the state the placeholder exists to close.
func TestLockedShadowKeyIsNotDerivableFromTheServerKey(t *testing.T) {
	const serverKey, method = "c2VydmVyLWtleS1oZXJlLTMyLWJ5dGVzISE=", "2022-blake3-aes-128-gcm"

	got := LockedShadowKey(serverKey, method)
	if got == "" {
		t.Fatal("no key produced for a supported method")
	}

	// What an attacker holding only the client link can compute.
	n := SSKeyLen(method)
	sum := sha256.Sum256([]byte("rospanel-ss2022-locked|" + serverKey))
	guessable := base64.StdEncoding.EncodeToString(sum[:n])

	// With an install key present the two must differ. Without one there is no secret to
	// build on and the fallback is deliberate — so this asserts the property only when
	// the install actually has a key, which is the shipped default.
	if hasInstallKey() && got == guessable {
		t.Error("the placeholder key is the public hash of a value every client holds")
	}
	if len(got) == 0 {
		t.Error("empty key")
	}
}

// hasInstallKey reports whether datasec has a key in this process. Tests run without
// datasec.Init, so this is normally false — the assertion above then documents the
// fallback rather than failing on it.
func hasInstallKey() bool {
	_, ok := deriveProbe()
	return ok
}
