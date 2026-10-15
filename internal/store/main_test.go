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
// testRoot is a directory the whole package's run may leave files in; TestMain removes it.
var testRoot string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "store-datasec")
	if err != nil {
		panic(err)
	}
	if err := datasec.Init(dir); err != nil {
		panic(err)
	}
	testRoot = dir // the pre-migration databases go here too, and go with it
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
