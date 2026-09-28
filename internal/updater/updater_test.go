package updater

import (
	"os"
	"path/filepath"
	"testing"
)

// Each install downloads into a file of its own, and that file can be run.
func TestDownloadFileIsOwnAndRunnable(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "rospanel")
	a, err := downloadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	b, err := downloadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two installs share one download file")
	}
	st, err := os.Stat(a)
	if err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("download file not executable: %v %v", st.Mode(), err)
	}
	if filepath.Dir(a) != filepath.Dir(exe) {
		t.Fatal("the download file is not beside the binary: the swap would cross filesystems")
	}
}

// A second install while one runs is refused, not interleaved.
func TestApplyRefusesAConcurrentInstall(t *testing.T) {
	applyMu.Lock()
	defer applyMu.Unlock()
	if err := Apply(t.Context(), &Release{AssetURL: "x", ChecksumURL: "y"}, nil); err != ErrInProgress {
		t.Fatalf("err = %v", err)
	}
}

// The version a binary reports is read from `<binary> version`.
func TestBinaryVersion(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "rospanel")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'rospanel v4.1.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v := binaryVersion(script); v != "4.1.0" {
		t.Fatalf("version = %q", v)
	}
	if v := binaryVersion(filepath.Join(dir, "missing")); v != "" {
		t.Fatalf("a missing binary reported %q", v)
	}
}
