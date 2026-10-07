package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func zipOf(t *testing.T, manifestJSON string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range map[string]string{"plugin.json": manifestJSON, "main.js": "export function onEvent() {}"} {
		f, _ := w.Create(name)
		_, _ = f.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func manifestFor(id, version, panel string) string {
	return `{"id": "` + id + `", "version": "` + version + `", "api": 1, "panel": "` + panel + `", "name": {"ru": "Тест", "en": "Test"},
		"author": "me", "permissions": ["users.view"], "provides": {"events": ["user.created"]}}`
}

// catalogDir writes a catalog folder: hello 1.0.0 (verified) and 1.2.0 (needs a newer panel).
func catalogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel string, b []byte) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plugins/hello/hello-1.0.0.zip", zipOf(t, manifestFor("hello", "1.0.0", ">=4.4.0")))
	write("plugins/hello/hello-1.2.0.zip", zipOf(t, manifestFor("hello", "1.2.0", ">=9.0.0")))
	write("verified.json", []byte(`{"hello": ["1.0.0"]}`))
	return dir
}

func TestBuildSignFetch(t *testing.T) {
	dir := catalogDir(t)
	priv, pub, err := Keygen()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Build(dir, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(raw, priv)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"https://c.example/x/index.json": raw, "https://c.example/x/index.json.sig": sig}
	fetches := 0
	fetch := func(_ context.Context, url string, _ int) ([]byte, error) {
		fetches++
		if b, ok := files[url]; ok {
			return b, nil
		}
		return nil, errors.New("404")
	}
	cache := t.TempDir()
	c := New(fetch, cache)
	c.keys = []string{pub}
	st := c.Get(context.Background(), "https://c.example/x/index.json", false)
	if st.Error != "" || st.Index == nil || len(st.Index.Plugins) != 1 {
		t.Fatalf("%+v", st)
	}
	e := st.Index.Lookup("hello")
	if e.Versions[0].Version != "1.2.0" || e.Versions[1].Verified != true || e.Versions[0].Verified {
		t.Fatalf("versions: %+v", e.Versions)
	}
	if l := e.Latest("4.5.0"); l == nil || l.Version != "1.0.0" {
		t.Fatalf("latest on 4.5.0: %+v", l)
	}
	if u, _ := PackageURL(st.URL, e.Latest("4.5.0")); u != "https://c.example/x/plugins/hello/hello-1.0.0.zip" {
		t.Fatalf("package url %s", u)
	}
	// Kept: no second fetch within RefreshEvery.
	c.Get(context.Background(), "https://c.example/x/index.json", false)
	if fetches != 2 {
		t.Fatalf("fetched %d times", fetches)
	}

	// The copy on disk serves a fresh panel while the catalog is unreachable.
	c2 := New(func(context.Context, string, int) ([]byte, error) { return nil, errors.New("down") }, cache)
	c2.keys = []string{pub}
	st = c2.Get(context.Background(), "https://c.example/x/index.json", true)
	if st.Index == nil || !strings.Contains(st.Error, "down") {
		t.Fatalf("offline: %+v", st)
	}

	// A changed index, or one signed by a key the panel does not trust, is refused.
	files["https://c.example/x/index.json"] = bytes.Replace(raw, []byte(`"verified": true`), []byte(`"verified": false`), 1)
	c3 := New(fetch, "")
	c3.keys = []string{pub}
	if st := c3.Get(context.Background(), "https://c.example/x/index.json", true); st.Index != nil || !strings.Contains(st.Error, "not signed") {
		t.Fatalf("tampered: %+v", st)
	}
	files["https://c.example/x/index.json"] = raw
	c4 := New(fetch, "") // the built-in keys only
	if st := c4.Get(context.Background(), "https://c.example/x/index.json", true); st.Index != nil {
		t.Fatal("an index signed by an unknown key was accepted")
	}
}

func TestBuildRefusesBadFolders(t *testing.T) {
	dir := catalogDir(t)
	_ = os.WriteFile(filepath.Join(dir, "verified.json"), []byte(`{"hello": ["3.0.0"]}`), 0o644)
	if _, err := Build(dir, time.Now()); err == nil || !strings.Contains(err.Error(), "3.0.0") {
		t.Fatalf("a verified version that is not there: %v", err)
	}
	dir = catalogDir(t)
	_ = os.MkdirAll(filepath.Join(dir, "plugins", "other"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "plugins", "other", "x.zip"), zipOf(t, manifestFor("hello", "2.0.0", "")), 0o644)
	if _, err := Build(dir, time.Now()); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("a package in another plugin's folder: %v", err)
	}
}

func TestOfficialKeyIsValid(t *testing.T) {
	if err := Verify([]byte("x"), []byte(strings.Repeat("A", 86)+"=="), TrustedKeys); err == nil {
		t.Fatal("a zero signature verified")
	}
	for _, k := range TrustedKeys {
		if len(k) != 44 {
			t.Fatalf("key %q is not a base64 ed25519 public key", k)
		}
	}
}
