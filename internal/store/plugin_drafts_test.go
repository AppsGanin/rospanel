package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestPluginDrafts(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := &model.PluginDraft{Name: "Welcome", Mode: model.DraftBuilder, PluginID: "welcome", Version: "1.0.0",
		Files: map[string][]byte{"main.js": []byte("export {}"), "theme/bg.png": {0, 1, 2}}, CreatedBy: "admin"}
	if err := st.SavePluginDraft(d); err != nil || d.ID == 0 {
		t.Fatal(d.ID, err)
	}
	got, err := st.GetPluginDraft(d.ID)
	if err != nil || string(got.Files["main.js"]) != "export {}" || len(got.Files["theme/bg.png"]) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	d.Name, d.Mode = "Welcome 2", model.DraftCode
	if err := st.SavePluginDraft(d); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListPluginDrafts()
	if err != nil || len(list) != 1 || list[0].Name != "Welcome 2" || list[0].Files != nil {
		t.Fatalf("%+v %v", list, err)
	}
	if err := st.DeletePluginDraft(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPluginDraft(d.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err := st.SavePluginDraft(d); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("saved over a deleted draft: %v", err)
	}
}
