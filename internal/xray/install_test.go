package xray

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// versionStub writes an executable that answers `version` the way Xray does.
func versionStub(t *testing.T, dir, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	p := filepath.Join(dir, "xray")
	script := "#!/bin/sh\necho 'Xray " + version + " (Xray, Penetrates Everything.) abc123 (go1.26.5 linux/amd64)'\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBinaryVersionReadsTheFirstLine(t *testing.T) {
	dir := t.TempDir()
	if got := binaryVersion(versionStub(t, dir, "26.7.28")); got != "26.7.28" {
		t.Errorf("binaryVersion = %q, want 26.7.28", got)
	}
	// Anything that won't run has no version — and must not be reported as one, or
	// EnsureBinary would compare "" against the pin and re-download on every boot.
	if got := binaryVersion(filepath.Join(dir, "absent")); got != "" {
		t.Errorf("missing binary: binaryVersion = %q, want empty", got)
	}
}

// The pinned release already sitting in place is used as is: no network, no
// re-download, no touching the file.
func TestEnsureBinaryKeepsThePinnedRelease(t *testing.T) {
	dir := t.TempDir()
	want := versionStub(t, dir, "26.7.28")
	if PinnedVersion != "v26.7.28" {
		t.Skipf("stub speaks 26.7.28; pin moved to %s", PinnedVersion)
	}
	before, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := EnsureBinary(dir)
	if err != nil || got != want {
		t.Fatalf("EnsureBinary = %q, %v; want %q, nil", got, err, want)
	}
	after, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the binary was rewritten even though it is the pinned release")
	}
}

// A failed upgrade must leave the working install alone: the box that can't reach
// GitHub keeps running the Xray it has, and the caller is told nothing is wrong.
// With nothing installed the same failure has to surface — the panel cannot run
// without Xray, and a silent empty path would only fail further along.
func TestOrKeepFailsOnlyWithNothingInstalled(t *testing.T) {
	boom := errors.New("no route to github")
	if err := orKeep("/var/lib/rospanel/bin/xray", boom); err != nil {
		t.Errorf("with a binary installed: orKeep = %v, want nil", err)
	}
	if err := orKeep("", boom); !errors.Is(err, boom) {
		t.Errorf("with nothing installed: orKeep = %v, want the download error", err)
	}
}
