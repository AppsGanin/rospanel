package opera

import (
	"testing"
)

func TestSupervisorLifecycle(t *testing.T) {
	sup := New("/nonexistent/opera-proxy")
	if sup == nil {
		t.Fatal("New returned nil")
	}

	if sup.Running() {
		t.Error("Running() initially = true; want false")
	}

	// Calling Stop() when inactive is safe
	sup.Stop()
	if sup.Running() {
		t.Error("Running() after Stop = true; want false")
	}
}
