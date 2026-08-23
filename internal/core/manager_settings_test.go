package core

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

func TestSaveSubSettingsCollisionValidation(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	mgr := New(st, sup, xray.Options{}, TLSPaths{}, dir)

	// Seed settings with distinct paths
	cur, err := st.GetSettings()
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	panelSecret := cur.PanelSecretPath
	if err := st.SetAPIPath("apiprefix"); err != nil {
		t.Fatalf("set api path: %v", err)
	}
	if err := st.SetNodeAPIPath("nodeprefix"); err != nil {
		t.Fatalf("set node api path: %v", err)
	}

	tests := []struct {
		name    string
		subPath string
		wantErr bool
	}{
		{"valid path", "mysubpath", false},
		{"same as panel secret", panelSecret, true},
		{"same as api path", "apiprefix", true},
		{"same as node api path", "nodeprefix", true},
		{"same as default status path", "status", true},
		{"reserved path", "login", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mgr.SaveSubSettings(&model.Settings{
				SubPath: tt.subPath,
			})
			if tt.wantErr && err == nil {
				t.Errorf("SaveSubSettings(%q) expected error, got nil", tt.subPath)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("SaveSubSettings(%q) unexpected error: %v", tt.subPath, err)
			}
		})
	}
}
