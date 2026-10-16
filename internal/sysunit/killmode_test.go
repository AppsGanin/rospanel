package sysunit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureKillModeMixed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mode := "control-group"
	var calls []string
	cmd := func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return mode + "\n", nil
	}
	if ensure("rospanel-node.service", dir, cmd) {
		t.Fatal("touched a unit that is not in the directory")
	}
	if err := os.WriteFile(filepath.Join(dir, "rospanel-node.service"), []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ensure("rospanel-node.service", dir, cmd) {
		t.Fatal("did not add the override")
	}
	b, err := os.ReadFile(filepath.Join(dir, "rospanel-node.service.d", dropInName))
	if err != nil || string(b) != dropIn {
		t.Fatalf("override %q (%v)", b, err)
	}
	if calls[len(calls)-1] != "daemon-reload" {
		t.Errorf("no daemon-reload after writing: %v", calls)
	}
	for _, m := range []string{"mixed", "process"} {
		mode, calls = m, nil
		if ensure("rospanel-node.service", dir, cmd) || len(calls) != 1 {
			t.Errorf("a unit on KillMode=%s was touched: %v", m, calls)
		}
	}
	RemoveDropIn(filepath.Join(dir, "rospanel-node.service"))
	if _, err := os.Stat(filepath.Join(dir, "rospanel-node.service.d")); !os.IsNotExist(err) {
		t.Error("uninstall left the override behind")
	}
}
