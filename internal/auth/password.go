// Package auth provides password hashing (Argon2id), random secret generation,
// and constant-time helpers used by the panel's authentication and masquerade.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. 64 MiB / t=1 / p=4 is a sane modern default; Configure()
// tunes memory + threads down on small VPS so concurrent logins don't OOM the box.
const (
	argonTime   = 1
	argonKeyLen = 32
	saltLen     = 16
)

// argonMemory (KiB) and argonThreads default to the strong setting and are lowered
// by Configure() on RAM-/CPU-constrained hosts. Argon parameters are embedded in
// each hash, so changing these only affects newly-created hashes — existing hashes
// keep verifying with the parameters they were minted under.
var (
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4

	// hashSem bounds how many Argon2id computations run at once. Each costs
	// argonMemory×… of RAM and a core's worth of CPU, so without a cap a burst of
	// login attempts (even rate-limited per IP, a distributed spray clears that)
	// could allocate gigabytes and stall the box. Sized to the CPU count (max 4) so
	// peak hashing memory is bounded to hashSemSize×argonMemory regardless of load.
	hashSem = make(chan struct{}, hashSemSize())
)

func hashSemSize() int {
	n := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	return n
}

// Configure tunes the Argon2id cost to the host so concurrent logins can't OOM a
// small VPS, while keeping the strong default on roomy hosts. Call once at startup
// before any password is hashed. Safe to skip (defaults stand).
func Configure() {
	switch total := totalRAMKiB(); {
	case total > 0 && total < 1024*1024: // < 1 GiB
		argonMemory = 32 * 1024
	case total > 0 && total < 2*1024*1024: // < 2 GiB
		argonMemory = 48 * 1024
	default:
		argonMemory = 64 * 1024
	}
	if n := runtime.NumCPU(); n >= 1 && n < int(argonThreads) {
		argonThreads = uint8(n)
	}
}

// totalRAMKiB reads MemTotal from /proc/meminfo (Linux). Returns 0 elsewhere or on
// error, which leaves Configure() on the strong default.
func totalRAMKiB() int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 {
			v, _ := strconv.ParseInt(f[1], 10, 64)
			return v
		}
	}
	return 0
}

// HashPassword returns a PHC-formatted Argon2id hash string.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hashSem <- struct{}{}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-hashSem
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches the PHC-encoded hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	hashSem <- struct{}{}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	<-hashSem
	return subtle.ConstantTimeCompare(got, want) == 1
}

// DummyVerify burns roughly one Argon2id computation so that login attempts for
// unknown usernames take similar time to real ones (anti-enumeration).
func DummyVerify() {
	salt := make([]byte, saltLen)
	hashSem <- struct{}{}
	_ = argon2.IDKey([]byte("dummy-password"), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-hashSem
}

var secretPathEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// RandomSecretPath returns a ~128-bit, URL-safe lowercase path segment.
func RandomSecretPath() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(secretPathEnc.EncodeToString(b)), nil
}

// RandomPassword returns a strong URL-safe random password.
func RandomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// RandomWSPath returns a random WebSocket path like "/3f9k2m".
func RandomWSPath() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "/" + strings.ToLower(secretPathEnc.EncodeToString(b)), nil
}

// RandomToken returns a 256-bit URL-safe opaque token (sessions).
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
