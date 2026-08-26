package i18n

import (
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/i18n/dictcheck"
)

// The generated catalog is a copy of the panel's English dictionary, and a copy is
// only safe while something checks it. Without this, adding an error code would leave
// the panel showing English and the external API answering Russian — the exact split
// the generator exists to prevent, and one nobody would notice until an integrator
// reported it.
func TestErrorCatalogIsCurrent(t *testing.T) {
	want, err := dictcheck.ErrorsEN(".")
	if err != nil {
		t.Skipf("frontend dictionary not available (%v)", err)
	}
	if len(want) == 0 {
		t.Fatal("no err.* entries parsed — did the dictionary shape change?")
	}
	for code, text := range want {
		got, ok := errEN[code]
		if !ok {
			t.Errorf("%s is in en.ts but not in the catalog — run `go generate ./internal/i18n/...`", code)
			continue
		}
		if got != text {
			t.Errorf("%s drifted:\n catalog: %q\n en.ts:   %q\nrun `go generate ./internal/i18n/...`", code, got, text)
		}
	}
	for _, code := range ErrorCodes() {
		if _, ok := want[code]; !ok {
			t.Errorf("%s is in the catalog but no longer in en.ts — run `go generate ./internal/i18n/...`", code)
		}
	}
}

// The placeholders are the half a plain copy can still get wrong: a message whose
// {{slot}} is never filled reaches the caller with braces in it.
func TestErrorENInterpolates(t *testing.T) {
	// A code with a slot, taken from the catalog itself so the test doesn't pin a
	// wording that is free to change.
	const code = "err.planHasUsers"
	raw, ok := errEN[code]
	if !ok {
		t.Skipf("%s is gone from the dictionary", code)
	}
	got, ok := ErrorEN(code, map[string]any{"count": 12})
	if !ok {
		t.Fatalf("%s did not resolve", code)
	}
	if got == raw {
		t.Fatalf("nothing was substituted in %q", got)
	}
	if _, ok := ErrorEN("err.doesNotExist", nil); ok {
		t.Error("an unknown code must report false so the caller keeps its fallback")
	}
}
