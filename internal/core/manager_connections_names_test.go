package core

import "testing"

// TestValidateConnNames covers the custom node-name rules: trimming, the safe
// charset, reserved tags, length, and the distinctness needed so sing-box/Clash
// selector tags don't collide.
func TestValidateConnNames(t *testing.T) {
	// Valid: custom names plus empties (which fall back to distinct defaults).
	got, err := validateConnNames(map[string]string{
		"vless":  "  Основной  ",
		"trojan": "Резерв",
	})
	if err != nil {
		t.Fatalf("valid names rejected: %v", err)
	}
	if got["vless"] != "Основной" {
		t.Fatalf("name not trimmed: %q", got["vless"])
	}
	if got["reality"] != "" {
		t.Fatalf("empty name should stay empty, got %q", got["reality"])
	}

	// Two protocols resolving to the same display name must be rejected.
	if _, err := validateConnNames(map[string]string{
		"vless":  "Main",
		"trojan": "main", // case-insensitive clash
	}); err == nil {
		t.Fatal("expected duplicate-name rejection")
	}

	// A custom name equal to another protocol's DEFAULT label collides too.
	if _, err := validateConnNames(map[string]string{
		"vless": "TROJAN-WS",
	}); err == nil {
		t.Fatal("expected clash with default label")
	}

	for _, bad := range []string{"auto", "direct", "bad\"quote", "no,comma", "a{b}"} {
		if _, err := validateConnNames(map[string]string{"vless": bad}); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
	}

	// Over 32 runes.
	long := ""
	for i := 0; i < 33; i++ {
		long += "x"
	}
	if _, err := validateConnNames(map[string]string{"vless": long}); err == nil {
		t.Fatal("expected length rejection")
	}
}
