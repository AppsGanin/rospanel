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

// One draft per plugin, and a save from an older read does not write over a newer
// one.
func TestPluginDraftGuards(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	files := map[string][]byte{"main.js": []byte("export {}")}
	a := &model.PluginDraft{Name: "A", Mode: model.DraftCode, PluginID: "same", Files: files}
	if err := st.SavePluginDraft(a); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePluginDraft(&model.PluginDraft{Name: "B", Mode: model.DraftCode, PluginID: "same", Files: files}); !errors.Is(err, ErrDraftExists) {
		t.Fatalf("a second draft of the plugin: %v", err)
	}
	// Drafts without an id yet do not collide.
	for range 2 {
		if err := st.SavePluginDraft(&model.PluginDraft{Name: "x", Mode: model.DraftCode, Files: files}); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := st.GetPluginDraft(a.ID)
	second, _ := st.GetPluginDraft(a.ID)
	first.Name = "edited"
	if err := st.SavePluginDraft(first); err != nil {
		t.Fatal(err)
	}
	second.Name = "stale"
	if err := st.SavePluginDraft(second); !errors.Is(err, ErrDraftChanged) {
		t.Fatalf("a save from an older read: %v", err)
	}
	if got, _ := st.GetPluginDraft(a.ID); got.Name != "edited" {
		t.Fatalf("the newer edit was lost: %q", got.Name)
	}
	if err := st.SavePluginDraft(first); err != nil { // its Rev moved on with the save
		t.Fatal(err)
	}
}
