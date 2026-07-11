package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LocalBackupDir is the subdirectory of the data dir holding scheduled local
// archives. WriteWithManifest skips it, so archives never nest inside each other.
const LocalBackupDir = "backups"

// localPrefix / localSuffix bracket the generated file names. Rotate only ever
// deletes files matching both, so anything else an operator drops in the directory
// (a manual copy, a restore they staged by hand) is left alone.
const (
	localPrefix = "rospanel-backup-"
	localSuffix = ".tar.gz"
)

// WriteLocal builds a backup archive into <dataDir>/backups and returns its path.
// checkpoint (may be nil) flushes the DB's WAL first so the archived DB is complete.
//
// The archive is written to a temp name in the same directory and renamed into
// place, so a crash mid-write can't leave a truncated file that looks like a valid
// backup — and so Rotate never sees a half-written archive as a rotation candidate.
func WriteLocal(dataDir string, m Manifest, checkpoint func() error, now time.Time) (string, error) {
	if checkpoint != nil {
		_ = checkpoint() // best-effort, same as the Telegram path
	}
	dir := filepath.Join(dataDir, LocalBackupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	final := filepath.Join(dir, localPrefix+now.Format("20060102-150405")+localSuffix)
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return "", err
	}
	werr := WriteWithManifest(dataDir, m, tmp)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return "", firstErr(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return final, nil
}

// Rotate deletes the oldest local archives, keeping the newest keep. keep <= 0
// disables rotation (retain everything) rather than deleting the lot — a zero value
// from an unset config must never be read as "erase every backup I have".
func Rotate(dataDir string, keep int) (removed int, err error) {
	if keep <= 0 {
		return 0, nil
	}
	dir := filepath.Join(dataDir, LocalBackupDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, localPrefix) && strings.HasSuffix(n, localSuffix) {
			names = append(names, n)
		}
	}
	if len(names) <= keep {
		return 0, nil
	}
	// The timestamp in the name sorts lexicographically in chronological order, so
	// this needs no stat() — and it stays correct if an archive's mtime is disturbed
	// by a copy or a restore.
	sort.Strings(names)

	for _, n := range names[:len(names)-keep] {
		if rerr := os.Remove(filepath.Join(dir, n)); rerr != nil && err == nil {
			err = rerr
			continue
		}
		removed++
	}
	return removed, err
}

// ListLocal returns the local archive file names, newest first.
func ListLocal(dataDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dataDir, LocalBackupDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, localPrefix) && strings.HasSuffix(n, localSuffix) {
			names = append(names, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

// firstErr returns the first non-nil error.
func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
