package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/plugin/catalog"
)

// The catalog lists a signed index, installs from it only the package the index
// names, and tells the operator once about a new version — installing nothing.
func TestPluginCatalog(t *testing.T) {
	rt, st := rolesTestRouter(t)
	host := plugin.New(plugin.Deps{Store: st, DataDir: rt.dataDir, PanelVersion: "4.4.0", Logger: slog.New(slog.DiscardHandler)})
	rt.SetPlugins(host)
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	var notes []string
	var mu sync.Mutex
	rt.mgr.SetAdminNotifier(func(html string) { mu.Lock(); notes = append(notes, html); mu.Unlock() })

	// A catalog folder with hello 1.0.0 (reviewed), signed by a test key.
	dir := t.TempDir()
	put := func(version string) {
		raw := pluginZip(t, map[string]string{
			"plugin.json": `{"id": "hello", "version": "` + version + `", "api": 1, "name": "Hello",
				"permissions": ["users.view"], "provides": {"events": ["user.created"]}}`,
			"main.js": `export function onEvent() {}`,
		})
		_ = os.MkdirAll(filepath.Join(dir, "plugins", "hello"), 0o755)
		if err := os.WriteFile(filepath.Join(dir, "plugins", "hello", "hello-"+version+".zip"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("1.0.0")
	_ = os.WriteFile(filepath.Join(dir, "verified.json"), []byte(`{"hello": ["1.0.0"]}`), 0o644)
	priv, pub, _ := catalog.Keygen()
	files := map[string][]byte{}
	publish := func() {
		raw, err := catalog.Build(dir, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		sig, _ := catalog.Sign(raw, priv)
		files["https://cat.example/index.json"], files["https://cat.example/index.json.sig"] = raw, sig
		for _, v := range []string{"1.0.0", "1.1.0"} {
			if b, err := os.ReadFile(filepath.Join(dir, "plugins", "hello", "hello-"+v+".zip")); err == nil {
				files["https://cat.example/plugins/hello/hello-"+v+".zip"] = b
			}
		}
	}
	publish()
	rt.pluginCatalog.keys = []string{pub}
	rt.pluginCatalog.fetch = func(_ context.Context, u string, _ int) ([]byte, error) {
		if b, ok := files[u]; ok {
			return b, nil
		}
		return nil, errors.New("404 " + u)
	}
	if err := st.SetPluginCatalogURL("https://cat.example/index.json"); err != nil {
		t.Fatal(err)
	}

	call := func(h http.HandlerFunc, method, body string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/plugins/catalog?refresh=1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		h(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != 200 {
			out["status"] = float64(w.Code)
		}
		return out
	}
	list := call(rt.getPluginCatalog, "GET", "")
	items, _ := list["plugins"].([]any)
	if len(items) != 1 || list["error"] != "" && list["error"] != nil {
		t.Fatalf("catalog: %v", list)
	}
	if it := items[0].(map[string]any); it["latest"].(map[string]any)["verified"] != true || it["installed"] != nil {
		t.Fatalf("item: %v", it)
	}

	// The package is fetched and checked against the index, then installed as usual.
	ins := call(rt.inspectCatalogPlugin, "POST", `{"id": "hello"}`)
	if ins["from_catalog"] != true || ins["verified"] != true {
		t.Fatalf("inspect: %v", ins)
	}
	raw, ok := rt.pluginUploads.take(ins["sha256"].(string))
	if !ok {
		t.Fatal("the inspected package is not kept for the install")
	}
	if _, err := host.Install(context.Background(), raw, plugin.Consent{SHA256: ins["sha256"].(string), Perms: []string{"users.view"}}); err != nil {
		t.Fatal(err)
	}

	// A package that is not the one the index names is refused.
	good := files["https://cat.example/plugins/hello/hello-1.0.0.zip"]
	files["https://cat.example/plugins/hello/hello-1.0.0.zip"] = append(bytes.Clone(good), 0)
	if out := call(rt.inspectCatalogPlugin, "POST", `{"id": "hello", "version": "1.0.0"}`); out["code"] != "err.pluginCatalogMismatch" {
		t.Fatalf("a changed package: %v", out)
	}
	files["https://cat.example/plugins/hello/hello-1.0.0.zip"] = good

	// 1.1.0 appears: the card says so, the operator is told once, nothing is installed.
	put("1.1.0")
	publish()
	list = call(rt.getPluginCatalog, "GET", "")
	it := list["plugins"].([]any)[0].(map[string]any)
	if it["installed"] != "1.0.0" || it["installed_verified"] != true || it["update"] != true {
		t.Fatalf("after 1.1.0: %v", it)
	}
	rt.CheckPluginUpdates(context.Background())
	rt.CheckPluginUpdates(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(notes) != 1 || !strings.Contains(notes[0], "1.1.0") {
		t.Fatalf("notices: %q", notes)
	}
	if info, _ := host.Get("hello"); info.Version != "1.0.0" {
		t.Fatalf("updated by itself to %s", info.Version)
	}
}
