package core

import "testing"

// Pasting a supergroup id by hand goes wrong exactly one way: Telegram shows the
// bare internal id, the API wants it prefixed with -100, and without the prefix
// every call reports the group as unreachable.
func TestNormalizeGroupID(t *testing.T) {
	if got, want := normalizeGroupID(1234567890), int64(-1001234567890); got != want {
		t.Errorf("bare id = %d, want %d", got, want)
	}
	// Already correct — must pass through untouched.
	if got := normalizeGroupID(-1001234567890); got != -1001234567890 {
		t.Errorf("prefixed id was rewritten to %d", got)
	}
	// A negative id is already in some -prefixed form; guessing which would risk
	// pointing support at a different chat, so it is left alone.
	if got := normalizeGroupID(-1234567890); got != -1234567890 {
		t.Errorf("negative id was rewritten to %d", got)
	}
	if got := normalizeGroupID(0); got != 0 {
		t.Errorf("zero = %d, want 0", got)
	}
}
