package store

import (
	"path/filepath"
	"testing"
)

// TestClaimRegistrationRequest is the atomic gate behind moderation approval: only
// one of several concurrent claims (double-click, two admins) may win.
func TestClaimRegistrationRequest(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "claim.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	req, err := s.CreateRegistrationRequest(42, "Петя", 1000)
	if err != nil || req == nil {
		t.Fatalf("create: %v", err)
	}
	// A duplicate request for the same chat is refused (one pending per chat).
	if _, err := s.CreateRegistrationRequest(42, "Петя", 1001); err != ErrRegistrationPending {
		t.Fatalf("duplicate create err = %v, want ErrRegistrationPending", err)
	}
	// First claim wins, second loses — exactly one winner.
	if ok, err := s.ClaimRegistrationRequest(req.ID); err != nil || !ok {
		t.Fatalf("first claim = %v/%v, want true", ok, err)
	}
	if ok, err := s.ClaimRegistrationRequest(req.ID); err != nil || ok {
		t.Fatalf("second claim = %v/%v, want false", ok, err)
	}
	if r, _ := s.GetRegistrationRequestByChat(42); r != nil {
		t.Fatal("request must be gone after a claim")
	}
}
