package logbuf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteSplitsLinesAndKeepsOnlyTheNewest(t *testing.T) {
	h := New()
	// One multi-line write and many single ones: both paths land in the ring.
	if n, err := h.Write([]byte("a\nb\nc\n")); err != nil || n != 6 {
		t.Fatalf("Write = %d, %v; want the full 6 bytes consumed", n, err)
	}
	if got := h.Tail(); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("Tail = %v", got)
	}
	for i := range bufferSize + 10 {
		fmt.Fprintf(h, "line %d\n", i)
	}
	got := h.Tail()
	if len(got) != bufferSize {
		t.Fatalf("Tail holds %d lines, want the cap %d", len(got), bufferSize)
	}
	// The oldest lines (a, b, c and the first 10 numbered ones) are gone; the
	// newest is still there.
	if got[0] != "line 10" || got[len(got)-1] != fmt.Sprintf("line %d", bufferSize+9) {
		t.Errorf("ring holds %q .. %q; the wrong end was dropped", got[0], got[len(got)-1])
	}
}

// Empty writes and bare newlines are what a logger emits between records; they
// must not become blank rows in the viewer, but they must still count as consumed
// or an io.MultiWriter would report a short write and stop the whole tee.
func TestWriteIgnoresBlankInputButConsumesIt(t *testing.T) {
	h := New()
	for _, p := range []string{"", "\n", "\n\n\n"} {
		if n, err := h.Write([]byte(p)); err != nil || n != len(p) {
			t.Errorf("Write(%q) = %d, %v; want %d, nil", p, n, err, len(p))
		}
	}
	if got := h.Tail(); len(got) != 0 {
		t.Errorf("Tail = %v, want nothing from blank writes", got)
	}
}

// Tail hands out a copy: a viewer sorting or trimming what it got must not
// rearrange the hub's ring.
func TestTailIsACopy(t *testing.T) {
	h := New()
	_, _ = h.Write([]byte("one\ntwo\n"))
	got := h.Tail()
	got[0] = "changed"
	if h.Tail()[0] != "one" {
		t.Error("mutating the Tail result changed the hub's buffer")
	}
}

func TestSubscribeSeesOnlyLinesWrittenAfterwards(t *testing.T) {
	h := New()
	_, _ = h.Write([]byte("before\n"))
	ch, unsub := h.Subscribe()
	_, _ = h.Write([]byte("after\n"))

	select {
	case line := <-ch:
		if line != "after" {
			t.Errorf("got %q, want only lines written after subscribing", line)
		}
	case <-time.After(time.Second):
		t.Fatal("no line delivered to the subscriber")
	}

	unsub()
	if _, open := <-ch; open {
		t.Error("channel still open after unsubscribe; the viewer's goroutine would never exit")
	}
	unsub() // a second call must not double-close
	_, _ = h.Write([]byte("later\n"))
}

// The logger is called from request handlers; a viewer that stopped reading must
// cost it nothing. Excess lines are dropped for that viewer, not queued forever.
func TestSlowSubscriberDoesNotBlockTheWriter(t *testing.T) {
	h := New()
	ch, unsub := h.Subscribe()
	defer unsub()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 400 {
			fmt.Fprintf(h, "%d\n", i)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on a subscriber that is not reading")
	}
	// Whatever fit in the channel is there; the rest was dropped, never blocked.
	if got := len(ch); got == 0 || got > 400 {
		t.Errorf("buffered %d lines for the slow subscriber", got)
	}
	if got := h.Tail(); len(got) != 400 {
		t.Errorf("ring holds %d lines, want all 400 regardless of the slow subscriber", len(got))
	}
}

func TestLocationDefaultsToLocalUntilSet(t *testing.T) {
	// Global state, restored so other tests in the package are not affected.
	prev := loc.Load()
	t.Cleanup(func() { loc.Store(prev) })
	loc.Store(nil)

	if Location() != time.Local {
		t.Error("with nothing set, Location() should be the server's local zone")
	}
	SetLocation(nil)
	if Location() != time.Local {
		t.Error("SetLocation(nil) must be a no-op, not a crash later in Format")
	}
	SetLocation(time.UTC)
	if Location() != time.UTC {
		t.Error("SetLocation(UTC) was not applied")
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Rotation keeps exactly maxBackups older files, numbered newest-first, and the
// oldest falls off the end — the shape every log shipper and operator expects.
func TestRotatingFileRotatesAndKeepsTheConfiguredBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "panel.log")
	w, err := NewRotatingFile(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for _, line := range []string{"aaaaa\n", "bbbbb\n", "ccccc\n", "ddddd\n"} {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("write %q: %v", line, err)
		}
	}
	// 6 bytes each with a 10-byte cap: every write after the first rotates.
	if got := readFile(t, path); got != "ddddd\n" {
		t.Errorf("active file = %q, want the latest line only", got)
	}
	if got := readFile(t, path+".1"); got != "ccccc\n" {
		t.Errorf(".1 = %q, want the previous file", got)
	}
	if got := readFile(t, path+".2"); got != "bbbbb\n" {
		t.Errorf(".2 = %q, want the one before", got)
	}
	if exists(path + ".3") {
		t.Error(".3 exists; the oldest backup was not dropped")
	}
}

// After a restart the writer picks up the existing file's size, so the cap keeps
// meaning what it says instead of resetting on every boot.
func TestRotatingFileResumesFromExistingSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.log")
	w, err := NewRotatingFile(path, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("123456\n"))
	_ = w.Close()

	w, err = NewRotatingFile(path, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, _ = w.Write([]byte("789\n"))
	if !exists(path + ".1") {
		t.Error("the reopened writer forgot the file was already 7 bytes and did not rotate")
	}
	if got := readFile(t, path); got != "789\n" {
		t.Errorf("active file = %q after the resumed rotation", got)
	}
}

// With no backups wanted the file is simply truncated in place; nothing named
// .1 must appear, or the "no backups" setting would still fill the disk.
func TestRotatingFileZeroBackupsTruncatesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.log")
	w, err := NewRotatingFile(path, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, _ = w.Write([]byte("abc\n"))
	_, _ = w.Write([]byte("def\n"))
	if got := readFile(t, path); got != "def\n" {
		t.Errorf("active file = %q, want it truncated before the second write", got)
	}
	if exists(path + ".1") {
		t.Error("a backup was created although maxBackups is 0")
	}
}

// A record larger than the whole cap is still written whole after a rotation:
// log lines are never split, and losing one would hide exactly the long stack
// trace someone is looking for.
func TestRotatingFileNeverSplitsARecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.log")
	w, err := NewRotatingFile(path, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	big := strings.Repeat("x", 20) + "\n"
	if n, err := w.Write([]byte(big)); err != nil || n != len(big) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got := readFile(t, path); got != big {
		t.Errorf("active file = %q, want the oversized record intact", got)
	}
}
