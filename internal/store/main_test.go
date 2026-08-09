package store

import (
	"os"
	"testing"

	"github.com/AppsGanin/rospanel/internal/datasec"
)

// datasec keeps its key in a package variable with no way back, so the FIRST test that
// calls Init changes how every test after it stores secrets — and tests that assert on
// raw columns then pass or fail depending on file names. Init once for the whole
// package instead: encryption is always on in production, so it should always be on
// here too.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "store-datasec")
	if err != nil {
		panic(err)
	}
	if err := datasec.Init(dir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
