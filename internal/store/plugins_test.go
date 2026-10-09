package store

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestPluginRoundtrip(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	if p, err := st.GetPlugin("hello"); p != nil || err != nil {
		t.Fatalf("missing plugin: %v %v", p, err)
	}
	want := model.Plugin{
		ID: "hello", Version: "1.0.0", Manifest: `{"id":"hello"}`, Package: []byte("zip"), SHA256: "abc",
		Status: model.PluginDisabled, GrantedPerms: []string{"users.view"}, GrantedNet: []string{"api.x.ru"},
		Config: map[string]string{"token": "s3cret"},
	}
	if err := st.SavePlugin(want); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetPlugin("hello")
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Version != "1.0.0" || string(got.Package) != "zip" || got.Config["token"] != "s3cret" ||
		strings.Join(got.GrantedPerms, ",") != "users.view" || got.InstalledAt == 0 {
		t.Fatalf("%+v", got)
	}
	// Settings are encrypted at rest.
	var raw string
	if err := st.db.QueryRow(`SELECT config FROM plugins WHERE id = 'hello'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "s3cret") {
		t.Fatalf("plugin config stored in the clear: %s", raw)
	}

	installed := got.InstalledAt
	got.Version, got.PrevVersion, got.PrevPackage = "1.1.0", "1.0.0", []byte("old")
	if err := st.SavePlugin(*got); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginState("hello", true, model.PluginActive, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginConfig("hello", map[string]string{"token": "new"}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListPlugins()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	p := list[0]
	if p.Version != "1.1.0" || string(p.PrevPackage) != "old" || !p.Enabled || p.Status != model.PluginActive ||
		p.Config["token"] != "new" || p.InstalledAt != installed {
		t.Fatalf("%+v", p)
	}
	if err := st.DeletePlugin("hello"); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPlugin("hello"); p != nil {
		t.Fatal("deleted plugin still there")
	}
}
