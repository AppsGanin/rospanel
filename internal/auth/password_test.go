package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	Configure()

	pass := "StrongSecretPassw0rd!123"
	hash, err := HashPassword(pass)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !strings.HasPrefix(hash, "$argon2id$v=") {
		t.Fatalf("hash format unexpected: %s", hash)
	}

	// Verify matching password
	if !VerifyPassword(hash, pass) {
		t.Error("VerifyPassword(hash, pass) = false; want true")
	}

	// Verify wrong password
	if VerifyPassword(hash, "WrongPassword") {
		t.Error("VerifyPassword(hash, WrongPassword) = true; want false")
	}

	// Verify malformed hashes
	if VerifyPassword("invalid-hash-string", pass) {
		t.Error("VerifyPassword(invalid) = true; want false")
	}
	if VerifyPassword("$argon2i$v=19$m=65536,t=1,p=4$abc$def", pass) {
		t.Error("VerifyPassword(argon2i) = true; want false")
	}
}

func TestDummyVerify(t *testing.T) {
	// DummyVerify should run without panic
	DummyVerify()
}
