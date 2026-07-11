package backup

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "panel.db"), []byte("db"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return dir
}

func TestWriteLocalCreatesArchive(t *testing.T) {
	dir := seedDataDir(t)
	now := time.Date(2026, 7, 11, 3, 0, 0, 0, time.UTC)

	path, err := WriteLocal(dir, Manifest{}, nil, now)
	if err != nil {
		t.Fatalf("WriteLocal: %v", err)
	}
	if got := filepath.Base(path); got != "rospanel-backup-20260711-030000.tar.gz" {
		t.Fatalf("archive name = %q", got)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("archive missing or empty: err=%v", err)
	}

	// No .partial-* leftovers from the atomic write.
	entries, _ := os.ReadDir(filepath.Join(dir, LocalBackupDir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".partial-") {
			t.Fatalf("temp file %q survived", e.Name())
		}
	}
}

// The archive must not contain previous archives, or each backup nests the last one
// and the directory grows geometrically.
func TestWriteLocalExcludesTheBackupDir(t *testing.T) {
	dir := seedDataDir(t)
	first, err := WriteLocal(dir, Manifest{}, nil, time.Date(2026, 7, 11, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	firstSize := mustSize(t, first)

	second, err := WriteLocal(dir, Manifest{}, nil, time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	// If backups/ were walked, the second archive would carry the first one inside it.
	if got := mustSize(t, second); got > firstSize*2 {
		t.Fatalf("second archive (%d B) dwarfs the first (%d B) — backups/ is being archived",
			got, firstSize)
	}
}

// Hand-rolled recovery copies in the data dir must not ride along in the archive.
// A full DB copy is not small, and operators name them both ways.
func TestWriteLocalExcludesRecoveryCopies(t *testing.T) {
	dir := seedDataDir(t)
	for _, name := range []string{
		"rospanel.db.bak",
		"rospanel.db.bak-20260710-230819",
		"rospanel.db.bak.20260710-230819",
		"config.json.new",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("copy"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	path, err := WriteLocal(dir, Manifest{}, nil, time.Date(2026, 7, 11, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("WriteLocal: %v", err)
	}
	for _, name := range listArchive(t, path) {
		if strings.Contains(name, ".bak") || strings.HasSuffix(name, ".new") {
			t.Errorf("archive carries recovery artifact %q", name)
		}
	}
}

// listArchive returns the entry names inside a backup tar.gz.
func listArchive(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()

	var names []string
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		names = append(names, h.Name)
	}
	return names
}

func TestRotateKeepsNewest(t *testing.T) {
	dir := seedDataDir(t)
	for _, day := range []int{10, 11, 12, 13} {
		if _, err := WriteLocal(dir, Manifest{}, nil,
			time.Date(2026, 7, day, 3, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("write day %d: %v", day, err)
		}
	}

	removed, err := Rotate(dir, 2)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed %d, want 2", removed)
	}

	left, err := ListLocal(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"rospanel-backup-20260713-030000.tar.gz", "rospanel-backup-20260712-030000.tar.gz"}
	if len(left) != len(want) {
		t.Fatalf("survivors = %v, want %v", left, want)
	}
	for i := range want {
		if left[i] != want[i] { // ListLocal is newest-first
			t.Fatalf("survivors = %v, want %v", left, want)
		}
	}
}

// keep <= 0 means "retain everything". Reading an unset config as "delete them all"
// would destroy exactly what the feature exists to preserve.
func TestRotateKeepZeroDeletesNothing(t *testing.T) {
	dir := seedDataDir(t)
	for _, day := range []int{10, 11} {
		if _, err := WriteLocal(dir, Manifest{}, nil,
			time.Date(2026, 7, day, 3, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	for _, keep := range []int{0, -1} {
		removed, err := Rotate(dir, keep)
		if err != nil || removed != 0 {
			t.Fatalf("Rotate(keep=%d) removed %d (err=%v), want 0", keep, removed, err)
		}
		if left, _ := ListLocal(dir); len(left) != 2 {
			t.Fatalf("Rotate(keep=%d) left %d archives, want 2", keep, len(left))
		}
	}
}

// Rotation only ever touches files it generated, so an operator's own copies in the
// directory survive.
func TestRotateIgnoresForeignFiles(t *testing.T) {
	dir := seedDataDir(t)
	for _, day := range []int{10, 11, 12} {
		if _, err := WriteLocal(dir, Manifest{}, nil,
			time.Date(2026, 7, day, 3, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	foreign := filepath.Join(dir, LocalBackupDir, "my-own-copy.tar.gz")
	if err := os.WriteFile(foreign, []byte("mine"), 0o600); err != nil {
		t.Fatalf("seed foreign: %v", err)
	}

	if _, err := Rotate(dir, 1); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("rotation deleted a file it did not create: %v", err)
	}
}

func TestListLocalOnMissingDir(t *testing.T) {
	names, err := ListLocal(t.TempDir())
	if err != nil || names != nil {
		t.Fatalf("ListLocal on a dir with no backups = %v, %v; want nil, nil", names, err)
	}
}

func mustSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Size()
}
