package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// TestValidateConnNames covers the custom node-name rules: trimming, the safe
// charset, reserved tags, length, and the distinctness needed so sing-box/Clash
// selector tags don't collide.
func TestValidateConnNames(t *testing.T) {
	// Valid: custom names plus empties (which fall back to distinct defaults).
	got, err := validateConnNames(map[string]string{
		"vless": "  Основной  ",
	}, nil)
	if err != nil {
		t.Fatalf("valid names rejected: %v", err)
	}
	if got["vless"] != "Основной" {
		t.Fatalf("name not trimmed: %q", got["vless"])
	}
	if got["reality"] != "" {
		t.Fatalf("empty name should stay empty, got %q", got["reality"])
	}

	// Two lanes resolving to the same display name must be rejected.
	if _, err := validateConnNames(map[string]string{
		"vless":     "Main",
		"hysteria2": "main", // case-insensitive clash
	}, nil); err == nil {
		t.Fatal("expected duplicate-name rejection")
	}

	// A custom name equal to another lane's DEFAULT label collides too.
	if _, err := validateConnNames(map[string]string{
		"vless": "HYSTERIA-UDP",
	}, nil); err == nil {
		t.Fatal("expected clash with default label")
	}

	for _, bad := range []string{"auto", "direct", "bad\"quote", "no,comma"} {
		if _, err := validateConnNames(map[string]string{"vless": bad}, nil); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
	}

	// A name carrying variables is a normal name here: the braces are syntax, and the
	// value they stand for is decided per user when the subscription is rendered.
	if _, err := validateConnNames(map[string]string{"vless": "{flag} VLESS ({left})"}, nil); err != nil {
		t.Fatalf("a templated name was rejected: %v", err)
	}

	// A built-in lane may not be renamed onto a name a custom inbound already holds:
	// both become node names in the same generated document, and a duplicate tag makes
	// a client reject the whole profile.
	if _, err := validateConnNames(map[string]string{"vless": "Резерв"}, []string{"Резерв"}); err == nil {
		t.Fatal("expected a clash with a custom inbound's name")
	}

	// Over 32 runes.
	long := ""
	for i := 0; i < 33; i++ {
		long += "x"
	}
	if _, err := validateConnNames(map[string]string{"vless": long}, nil); err == nil {
		t.Fatal("expected length rejection")
	}
}

// The Salamander key is minted, never typed: the editor shows it read-only and the
// only thing that changes it is a request to regenerate. resolveObfs is where that is
// decided, so it is where it has to hold — including the part that makes the read-only
// field honest, that a regenerate ignores whatever value came with it.
func TestResolveObfs(t *testing.T) {
	// Regenerating wins over anything submitted, valid or not.
	for _, submitted := range []string{"", "abcdefgh12345678", "!! not valid !!"} {
		got, err := resolveObfs(submitted, true)
		if err != nil {
			t.Fatalf("regen with %q: %v", submitted, err)
		}
		if got == submitted {
			t.Errorf("regen returned the submitted value %q", submitted)
		}
		if !model.ValidObfsPassword(got) {
			t.Errorf("the generated key %q fails the panel's own validation", got)
		}
	}
	// Two regenerations never produce the same key.
	a, _ := resolveObfs("", true)
	b, _ := resolveObfs("", true)
	if a == b {
		t.Error("two regenerations produced the same key")
	}
	// Without a regenerate the value round-trips, and empty is how obfuscation is off.
	if got, err := resolveObfs("  abcdefgh12345678  ", false); err != nil || got != "abcdefgh12345678" {
		t.Errorf("round-trip = %q, %v", got, err)
	}
	if got, err := resolveObfs("", false); err != nil || got != "" {
		t.Errorf("empty = %q, %v — want it accepted as off", got, err)
	}
	// A hand-made request is still refused: a key the client cannot reproduce is a
	// lane nobody can connect to.
	if _, err := resolveObfs("short", false); err == nil {
		t.Error("a too-short key was accepted")
	}
	if _, err := resolveObfs("has spaces here!", false); err == nil {
		t.Error("a key outside the charset was accepted")
	}
}
