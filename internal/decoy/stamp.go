package decoy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// seedFile holds the per-install decoy entropy, next to the database.
const seedFile = "decoy.seed"

// Stamp is the per-install entropy applied to a decoy template. Every bundled
// template is byte-identical in every RosPanel binary, so without it the hash of
// the served index page is a fleet-wide identifier: collect it from one visible
// install and the rest fall out of a body-hash search on Censys/FOFA/Shodan.
//
// The stamp derives, from one random per-server seed:
//
//   - an insignificant HTML comment appended to every page (unique body bytes),
//   - a distinct plausible modification time per file (Last-Modified),
//   - the ETag that follows from that time and size.
//
// Two installs therefore share neither a body hash nor a validator, while each
// one stays perfectly stable across restarts — an ordinary site whose pages keep
// changing timestamp on every reload would be its own tell.
type Stamp struct {
	seed []byte
	base time.Time // newest plausible file time; per-file times sit below it
}

var (
	stampMu    sync.Mutex
	stampCache = map[string]Stamp{}
)

// LoadStamp returns the decoy stamp for dataDir, creating the seed on first use.
// Cached per directory so the repeated calls behind a template switch don't
// re-read the file.
//
// It never fails: a seed that cannot be persisted falls back to a process-lifetime
// random one, which still separates this install from every other — it just stops
// being stable across a restart.
func LoadStamp(dataDir string) Stamp {
	stampMu.Lock()
	defer stampMu.Unlock()
	if s, ok := stampCache[dataDir]; ok {
		return s
	}
	s := loadOrCreateSeed(dataDir)
	stampCache[dataDir] = s
	return s
}

// loadOrCreateSeed reads the seed file, generating it when absent. The file's own
// mtime anchors the stamp's time base: a site built shortly before it was deployed
// is what an ordinary deployment looks like.
func loadOrCreateSeed(dataDir string) Stamp {
	p := filepath.Join(dataDir, seedFile)
	if raw, err := os.ReadFile(p); err == nil && len(raw) >= 2*sha256.Size {
		seed, err := hex.DecodeString(string(raw[:2*sha256.Size]))
		if err == nil {
			base := time.Now()
			if fi, err := os.Stat(p); err == nil {
				base = fi.ModTime()
			}
			return newStamp(seed, base)
		}
	}
	seed := make([]byte, sha256.Size)
	if _, err := rand.Read(seed); err != nil {
		// crypto/rand does not fail in practice; if it ever did, an all-zero seed
		// would silently make every install identical again — the exact thing this
		// exists to prevent. Fall back to time, which at least still differs.
		binary.BigEndian.PutUint64(seed, uint64(time.Now().UnixNano()))
	}
	if err := os.WriteFile(p, []byte(hex.EncodeToString(seed)), 0o600); err != nil {
		slog.Warn("decoy: could not persist the site seed; page checksums will change on restart", "err", err)
	}
	return newStamp(seed, time.Now())
}

func newStamp(seed []byte, anchor time.Time) Stamp {
	s := Stamp{seed: seed}
	// Push the newest file a plausible 1–90 days before the anchor: sites are built
	// before they are deployed, not during the deploy.
	s.base = anchor.Add(-time.Duration(1+s.u64("base")%90) * 24 * time.Hour).Truncate(time.Second)
	return s
}

// u64 derives a stable 64-bit value from the seed and a label, so each use
// (comment length, per-file time, …) draws independently.
func (s Stamp) u64(label string) uint64 {
	h := sha256.New()
	h.Write([]byte(label))
	h.Write([]byte{0})
	h.Write(s.seed)
	return binary.BigEndian.Uint64(h.Sum(nil)[:8])
}

// modTime returns the plausible modification time of one file: somewhere in the
// month before the stamp's base, distinct per name. Real build output does not
// share a single timestamp across every file.
func (s Stamp) modTime(name string) time.Time {
	off := time.Duration(s.u64("mtime\x00"+name) % uint64(30*24*time.Hour))
	return s.base.Add(-off).Truncate(time.Second)
}

// mark is the per-install HTML comment. It reads as an ordinary build stamp, and
// its length varies too, so neither the body hash nor the body length lines up
// with another install's.
func (s Stamp) mark() string {
	n := int(6 + s.u64("marklen")%19) // 6–24 hex chars
	sum := sha256.Sum256(append([]byte("mark\x00"), s.seed...))
	return "<!-- " + hex.EncodeToString(sum[:])[:n] + " -->"
}
