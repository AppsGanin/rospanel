package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
	"github.com/AppsGanin/rospanel/internal/plugin/pdb"
)

// Probe loads a package the way a start does — its migrations on a scratch
// database, main.js after the prelude, the exports plugin.json declares — and runs
// none of them: what the editor's "check" answers beyond the manifest. A syntax
// error, a throw at the top of main.js, a missing export or a migration that does
// not apply is found here rather than when the plugin is switched on.
//
// While main.js loads, panel.config is empty and the rest of panel.* is not there:
// code at the top of a module has no business reaching the panel.
func (h *Host) Probe(ctx context.Context, raw []byte) error {
	pkg, err := manifest.Read(raw, h.deps.PanelVersion)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "rospanel-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := pdb.Open(ctx, filepath.Join(dir, "probe.db"), pkg.Manifest.Quota())
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Migrate(ctx, pkg.Migrations); err != nil {
		return err
	}
	vm, err := h.engine.NewVM(ctx, jsvm.Limits{Memory: pkg.Manifest.Memory()})
	if err != nil {
		return err
	}
	defer vm.Close()
	info, _ := json.Marshal(map[string]string{"id": pkg.Manifest.ID, "version": pkg.Manifest.Version})
	host := func(_ context.Context, op string, _ []byte) ([]byte, error) {
		switch op {
		case "plugin.info":
			return info, nil
		case "config.get":
			return []byte("{}"), nil
		case "log":
			return []byte("null"), nil
		}
		return nil, fmt.Errorf("panel.%s is not available while main.js loads — call it inside an export", op)
	}
	if err := vm.Script(ctx, "prelude.js", preludeJS, LoadTimeout, host); err != nil {
		return fmt.Errorf("prelude: %w", err)
	}
	if err := vm.Load(ctx, pkg.Main, LoadTimeout, host); err != nil {
		return fmt.Errorf("main.js: %s", withStack(err))
	}
	var missing []string
	for _, e := range pkg.Manifest.Exports() {
		if !vm.Has(ctx, e) {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("main.js does not export what plugin.json declares: %s", strings.Join(missing, ", "))
	}
	return nil
}

// withStack is an error with where in the code it happened, when the VM knows.
func withStack(err error) string {
	if je, ok := err.(*jsvm.JSError); ok && je.Stack != "" {
		return je.Message + "\n" + strings.TrimSpace(je.Stack)
	}
	return err.Error()
}
