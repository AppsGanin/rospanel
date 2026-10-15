package server

import (
	"os"
	"testing"

	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
)

// TestMain gives every plugin engine of the package's tests one compiled-guest cache:
// each test's own data directory would compile the guest again, seconds apiece under
// the race detector.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rospanel-wasm-cache-")
	if err != nil {
		panic(err)
	}
	jsvm.CacheDirOverride = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
