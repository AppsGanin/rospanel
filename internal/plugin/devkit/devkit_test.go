package devkit

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// The template is what every author starts from: it must pass its own tests and
// pack into a package the panel accepts.
func TestTemplateWorks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hello-world")
	if _, err := New(dir, "hello-world", "4.4.0"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	results, err := RunTests(context.Background(), dir, "4.4.0", &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("the template has no tests")
	}
	for _, r := range results {
		if !r.OK {
			t.Errorf("%s: %s\n%s", r.Name, r.Error, out.String())
		}
	}
	raw, skipped, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := manifest.Read(raw, "4.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Manifest.ID != "hello-world" || len(pkg.Migrations) != 1 {
		t.Fatalf("%+v", pkg.Manifest)
	}
	if s := strings.Join(skipped, ","); !strings.Contains(s, "test.js") || !strings.Contains(s, "rospanel.d.ts") {
		t.Fatalf("author files went into the package (skipped only %s)", s)
	}
	// The same sources make the same zip.
	again, _, _ := Pack(dir)
	if !bytes.Equal(raw, again) {
		t.Fatal("pack is not reproducible")
	}
	if _, err := New(dir, "other", "4.4.0"); err == nil {
		t.Fatal("New wrote into a non-empty directory")
	}
	if _, err := New(t.TempDir(), "Bad_ID", "4.4.0"); err == nil {
		t.Fatal("New accepted a bad id")
	}
}

func TestTestsSeeFailuresAndMocks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	if _, err := New(dir, "mocky", "4.4.0"); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	man, _ := os.ReadFile(filepath.Join(dir, "plugin.json"))
	write("plugin.json", strings.Replace(string(man), `"net": []`, `"net": ["api.example.com"]`, 1))
	write("main.js", `
export function onEvent() {}
export function daily() {}
export function lookup(id) { return panel.users.get(id).body.name; }
export function remote() { return JSON.parse(panel.http.fetch("https://api.example.com/x").body).v; }
export function offList() { return panel.http.fetch("https://evil.com/x"); }
export function boom() { throw new TypeError("nope"); }
`)
	write("test.js", `
test("api mock", () => { mock.api("GET", "/v1/users/*", { body: { name: "Ann" } }); assert.equal(plugin.call("lookup", 7), "Ann"); });
test("http mock", () => { mock.http("https://api.example.com/", { body: { v: 42 } }); assert.equal(plugin.call("remote"), 42); assert.equal(mock.calls().length, 1); });
test("mocks reset between tests", () => { assert.throws(() => plugin.call("remote")); });
test("a mock does not open the allowlist", () => { mock.http("https://evil.com/", { body: "x" }); assert.throws(() => plugin.call("offList")); });
test("plugin errors reach the test", () => { plugin.call("boom"); });
test("a failing assertion", () => { assert.equal({a: [1, 2]}, {a: [1, 3]}); });
`)
	var out bytes.Buffer
	results, err := RunTests(context.Background(), dir, "4.4.0", &out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"api mock": true, "http mock": true, "mocks reset between tests": true,
		"a mock does not open the allowlist": true, "plugin errors reach the test": false, "a failing assertion": false}
	for _, r := range results {
		if want[r.Name] != r.OK {
			t.Errorf("%s: ok=%v (%s)", r.Name, r.OK, r.Error)
		}
	}
	if !strings.Contains(out.String(), "TypeError: nope") || !strings.Contains(out.String(), `expected {"a":[1,3]}`) {
		t.Fatalf("report:\n%s", out.String())
	}
}

func TestCheckMigrations(t *testing.T) {
	read := func(files map[string]string) *manifest.Package {
		dir := t.TempDir()
		files["plugin.json"] = `{"id":"mig","version":"1.0.0","api":1,"name":"M","provides":{}}`
		files["main.js"] = `export {}`
		for name, body := range files {
			p := filepath.Join(dir, name)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte(body), 0o644)
		}
		raw, _, err := Pack(dir)
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := manifest.Read(raw, "")
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}
	v1 := read(map[string]string{"migrations/0001.sql": "CREATE TABLE a(x)"})
	if err := CheckMigrations(v1, read(map[string]string{"migrations/0001.sql": "CREATE TABLE a(x)", "migrations/0002.sql": "CREATE TABLE b(x)"})); err != nil {
		t.Fatalf("an added migration was refused: %v", err)
	}
	if err := CheckMigrations(v1, read(map[string]string{"migrations/0001.sql": "CREATE TABLE a(y)"})); err == nil {
		t.Fatal("an edited migration passed")
	}
	if err := CheckMigrations(v1, read(map[string]string{})); err == nil {
		t.Fatal("a removed migration passed")
	}
}

// The example plugins in examples/plugins are what authors copy from: each must
// pass its own test.js and validate on this panel.
func TestExamplePlugins(t *testing.T) {
	root := filepath.Join("..", "..", "..", "examples", "plugins")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			var out bytes.Buffer
			results, err := RunTests(context.Background(), dir, "4.4.0", &out)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range results {
				if !r.OK {
					t.Errorf("%s: %s", r.Name, r.Error)
				}
			}
		})
	}
}
