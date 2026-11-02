package store

import (
	"bytes"
	"path/filepath"
	"testing"
)

// The manual method's picture is one row: stored bytes come back whole, the version
// changes whenever the bytes do (that is what keeps a cached URL honest), and delete
// leaves nothing behind — including when there was nothing to delete.
func TestManualPayImageRoundTrip(t *testing.T) {
	t.Parallel()
	st, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if v, err := st.ManualPayImageVersion(); err != nil || v != "" {
		t.Fatalf("fresh store: version %q, %v", v, err)
	}
	if b, err := st.ManualPayImage(); err != nil || b != nil {
		t.Fatalf("fresh store: %d bytes, %v", len(b), err)
	}

	first := []byte("\x89PNG first picture")
	v1, err := st.SetManualPayImage(first)
	if err != nil || v1 == "" {
		t.Fatalf("set: version %q, %v", v1, err)
	}
	if b, err := st.ManualPayImage(); err != nil || !bytes.Equal(b, first) {
		t.Fatalf("read back: %q, %v", b, err)
	}
	if v, err := st.ManualPayImageVersion(); err != nil || v != v1 {
		t.Fatalf("version = %q, want %q (%v)", v, v1, err)
	}

	// Same bytes again: the same version, so a re-upload of the same file does not
	// invalidate every cached copy. Different bytes: a different version.
	if v, _ := st.SetManualPayImage(first); v != v1 {
		t.Errorf("re-uploading the same bytes changed the version: %q → %q", v1, v)
	}
	second := []byte("\x89PNG second picture")
	v2, err := st.SetManualPayImage(second)
	if err != nil || v2 == v1 {
		t.Errorf("replacement version = %q (was %q), %v", v2, v1, err)
	}
	if b, _ := st.ManualPayImage(); !bytes.Equal(b, second) {
		t.Error("the replacement did not replace the picture")
	}

	if err := st.DeleteManualPayImage(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if b, _ := st.ManualPayImage(); b != nil {
		t.Error("the picture survived the delete")
	}
	if err := st.DeleteManualPayImage(); err != nil {
		t.Errorf("deleting nothing: %v", err)
	}
}
