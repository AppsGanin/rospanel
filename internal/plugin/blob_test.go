package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const blobManifest = `{"id": "blobby", "version": "1.0.0", "api": 1, "name": "Blobby",
	"permissions": ["users.view"], "net": ["up.example.com"]}`

const blobMain = `
export function backup() {
	const r = panel.api("GET", "/v1/backup", null, { blob: true });
	return { status: r.status, size: r.body.size, text: panel.blob.text(r.body), sha: panel.blob.hash(r.body, "sha256") };
}
export function upload() {
	const b = panel.blob.from("file body");
	const raw = panel.http.fetch("https://up.example.com/raw", { method: "PUT", body: b });
	const form = panel.http.fetch("https://up.example.com/form", { method: "POST", form: { chat_id: 42, document: { blob: b, filename: "backup.tar.gz", type: "application/gzip" } } });
	const back = panel.http.fetch("https://up.example.com/get", { blob: true });
	panel.kv.set("handle", b);
	return { raw: raw.body, form: form.body, back: panel.blob.text(back.body), backSize: back.body.size };
}
export function stale() { return panel.blob.text(panel.kv.get("handle")); }
export function many() { for (let i = 0; i < 17; i++) panel.blob.from("x"); }
`

func TestBlobs(t *testing.T) {
	h := newHarness(t)
	h.install(pkg(t, blobManifest, blobMain, nil))
	h.enable("blobby")

	got := h.mustCall("blobby", "backup", nil)
	if !strings.Contains(got, `"size":25`) || !strings.Contains(got, `\"name\": \"bob\"`) ||
		!strings.Contains(got, `"sha":"`) {
		t.Fatalf("backup: %s", got)
	}

	got = h.mustCall("blobby", "upload", nil)
	if !strings.Contains(got, `"raw":"allow=up.example.com PUT file body"`) {
		t.Fatalf("raw body: %s", got)
	}
	for _, want := range []string{`name=\"chat_id\"`, `filename=\"backup.tar.gz\"`, `Content-Type: application/gzip`, `file body`} {
		if !strings.Contains(got, want) {
			t.Fatalf("form: no %s in %s", want, got)
		}
	}
	if !strings.Contains(got, `"back":"allow=up.example.com GET "`) {
		t.Fatalf("answer into a blob: %s", got)
	}

	// A blob lives for one call: its files are gone, its handle is dead.
	entries, _ := os.ReadDir(filepath.Join(h.dir, "plugins", ".blobs"))
	if len(entries) != 0 {
		t.Fatalf("blobs left after the call: %v", entries)
	}
	if _, err := h.call("blobby", "stale", nil); err == nil || !strings.Contains(err.Error(), "not a blob of this call") {
		t.Fatalf("a handle from another call: %v", err)
	}
	if _, err := h.call("blobby", "many", nil); err == nil || !strings.Contains(err.Error(), "at most 16") {
		t.Fatalf("17 blobs: %v", err)
	}
}
