package core

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// TestFrontIsTheMastersOnly: with the front on, only the master's own config puts the
// TCP-TLS lane on loopback. The options every node's config is built from must never
// carry it — an agent from before the front would apply such a config and leave its
// :443 with nothing listening.
func TestFrontIsTheMastersOnly(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	dir := t.TempDir()
	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	withFront := New(m.store, sup, xray.Options{PanelDest: "127.0.0.1:8080", FrontVLESS: true}, TLSPaths{}, filepath.Join(dir, "opera"))
	t.Cleanup(withFront.Close)

	if withFront.genOpts().FrontVLESS {
		t.Fatal("the shared generation options carry the front: every node's config would too")
	}
	local, err := withFront.genOptsFor(model.LocalNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if !local.FrontVLESS {
		t.Fatal("the master's own config is not put behind its front")
	}
	node, err := withFront.genOptsFor(2)
	if err != nil {
		t.Fatal(err)
	}
	if node.FrontVLESS {
		t.Fatal("a node's config is put behind the master's front")
	}
}
