package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/tlsutil"
)

// What the certificate file says is read once per file: while the file is the same one
// the kept facts answer, and a certificate issued over it, renewed in place or taken
// away is seen on the very next ask.
func TestCertFactsAreKeptWhileTheFileIsTheSame(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cert.pem")
	writeCert := func(host string) []byte {
		t.Helper()
		pem, _, err := tlsutil.GenerateSelfSigned(host)
		if err != nil {
			t.Fatal(err)
		}
		tmp := path + ".new"
		if err := os.WriteFile(tmp, pem, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil { // issued over it: a new file
			t.Fatal(err)
		}
		return pem
	}
	var c certFacts
	pem := writeCert("a.example")
	first := c.of(path)
	if first.info == nil || first.pin == "" {
		t.Fatalf("facts of a readable certificate: %+v", first)
	}

	// The same file, rewritten in place with the same length and its time put back:
	// nothing a stat can see moved, so the kept facts must be what answers.
	fi, _ := os.Stat(path)
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(pem)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := c.of(path); got.pin != first.pin || got.info == nil {
		t.Errorf("an unchanged file was read again: %+v", got)
	}

	// Renewed in place: the modification time moves, and the file is read again.
	if err := os.Chtimes(path, fi.ModTime().Add(time.Minute), fi.ModTime().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := c.of(path); got.info != nil || got.pin != "" {
		t.Errorf("a rewritten file kept the old facts: %+v", got)
	}

	// Issued over it: a new file, new facts — even one put in place with the old file's
	// modification time (cp -p, rsync -t and tar all keep one): it is another inode.
	kept := fi.ModTime().Add(time.Minute)
	writeCert("b.example")
	if err := os.Chtimes(path, kept, kept); err != nil {
		t.Fatal(err)
	}
	second := c.of(path)
	if second.info == nil || second.pin == "" || second.pin == first.pin {
		t.Errorf("a new certificate was not read: %+v (first pin %s)", second, first.pin)
	}

	// Gone: nothing to trust and nothing to pin, kept facts or not.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := c.of(path); got.info != nil || got.pin != "" {
		t.Errorf("a missing certificate still answered: %+v", got)
	}
}
