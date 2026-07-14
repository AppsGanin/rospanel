package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A flushable ResponseWriter wrapped by the audit middleware's auditStatus must
// still be recognized as streamable — the SSH-provision POST is audited, so its
// writer is wrapped, and a direct type assertion would miss the Flusher.
func TestUnwrapFlusherThroughAuditWrapper(t *testing.T) {
	rec := httptest.NewRecorder() // implements http.Flusher
	wrapped := &auditStatus{ResponseWriter: rec, code: http.StatusOK}
	if _, ok := unwrapFlusher(wrapped); !ok {
		t.Fatal("unwrapFlusher failed to find the Flusher through auditStatus")
	}
	// A plain (unwrapped) flushable writer still works.
	if _, ok := unwrapFlusher(rec); !ok {
		t.Fatal("unwrapFlusher failed on a bare Flusher")
	}
	// A non-flushable, non-unwrappable writer is correctly reported as unsupported.
	if _, ok := unwrapFlusher(nopWriter{}); ok {
		t.Fatal("unwrapFlusher wrongly reported a non-flushable writer as streamable")
	}
}

type nopWriter struct{}

func (nopWriter) Header() http.Header         { return http.Header{} }
func (nopWriter) Write(b []byte) (int, error) { return len(b), nil }
func (nopWriter) WriteHeader(int)             {}
